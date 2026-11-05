// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package stack

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"forgejo.org/models"
	"forgejo.org/models/db"
	git_model "forgejo.org/models/git"
	issues_model "forgejo.org/models/issues"
	access_model "forgejo.org/models/perm/access"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/structs"
	"forgejo.org/modules/translation"
	notify_service "forgejo.org/services/notify"
	pull_service "forgejo.org/services/pull"
)

// Status is what the stack map shows per layer next to the title.
type Status struct {
	HeadSHA string
	Draft   bool
	Checks  structs.CommitStatusState // "" when the head commit has no statuses
}

// LoadStatus returns the head commit, draft flag and combined commit status of every layer.
func (s *Stack) LoadStatus(ctx context.Context) (map[int64]*Status, error) {
	res := make(map[int64]*Status, len(s.Layers))
	if err := s.Root.PR.LoadBaseRepo(ctx); err != nil {
		return nil, err
	}
	gitRepo, closer, err := gitrepo.RepositoryFromContextOrOpen(ctx, s.Root.PR.BaseRepo)
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	for _, l := range s.Layers {
		st := &Status{Draft: l.PR.IsWorkInProgress(ctx)}
		res[l.PR.ID] = st
		if l.PR.Flow != issues_model.PullRequestFlowGithub || l.PR.HeadRepoID != l.PR.BaseRepoID {
			continue // a lone fork or AGit PR: no stack actions, no head lookup in the base repo
		}
		if st.HeadSHA, err = gitRepo.GetBranchCommitID(l.PR.HeadBranch); err != nil {
			st.HeadSHA = "" // branch deleted: shown without checks
			continue
		}
		statuses, _, err := git_model.GetLatestCommitStatus(ctx, l.PR.BaseRepoID, st.HeadSHA, db.ListOptionsAll)
		if err != nil {
			return nil, err
		}
		if len(statuses) > 0 {
			st.Checks = git_model.CalcCommitStatus(statuses).State
		}
	}
	return res, nil
}

// ErrNotAllowed means the doer may not rebase every layer of the stack.
var ErrNotAllowed = errors.New("not allowed to update every layer of the stack")

// ErrStale means the stack changed after the page was loaded (a layer was closed,
// merged, retargeted or is missing from the expected heads).
var ErrStale = errors.New("the stack changed")

// LayerError tells which layer stopped a stack action.
type LayerError struct {
	PR  *issues_model.PullRequest
	Err error
}

func (e *LayerError) Error() string { return fmt.Sprintf("#%d: %v", e.PR.Index, e.Err) }
func (e *LayerError) Unwrap() error { return e.Err }

// Message is the user-facing reason, translated where Forgejo already has a string.
func (e *LayerError) Message(locale translation.Locale) string {
	var reason string
	var rebaseErr models.ErrRebaseConflicts
	var mergeErr models.ErrMergeConflicts
	switch {
	case errors.As(e.Err, &rebaseErr):
		reason = locale.TrString("damast.stack.reason_rebase_conflict", rebaseErr.CommitSHA)
	case errors.As(e.Err, &mergeErr):
		reason = locale.TrString("repo.pulls.merge_conflict")
	case errors.Is(e.Err, ErrStale), models.IsErrSHADoesNotMatch(e.Err), git.IsErrPushOutOfDate(e.Err):
		reason = locale.TrString("damast.stack.reason_moved")
	case git.IsErrPushRejected(e.Err):
		reason = locale.TrString("repo.pulls.push_rejected_no_message")
	case errors.Is(e.Err, ErrNotAllowed), errors.Is(e.Err, pull_service.ErrUserNotAllowedToMerge):
		reason = locale.TrString("repo.pulls.update_not_allowed")
	case errors.Is(e.Err, pull_service.ErrIsWorkInProgress):
		reason = locale.TrString("repo.pulls.no_merge_wip")
	case errors.Is(e.Err, pull_service.ErrNotMergeableState), errors.Is(e.Err, pull_service.ErrIsChecking), models.IsErrDisallowedToMerge(e.Err):
		reason = locale.TrString("repo.pulls.no_merge_not_ready")
	case errors.Is(e.Err, pull_service.ErrDependenciesLeft):
		reason = locale.TrString("repo.issues.dependency.pr_close_blocked")
	case errors.Is(e.Err, pull_service.ErrIsClosed), errors.Is(e.Err, pull_service.ErrHasMerged):
		reason = locale.TrString("damast.stack.reason_moved")
	case models.IsErrInvalidMergeStyle(e.Err):
		reason = locale.TrString("repo.pulls.invalid_merge_option")
	default:
		log.Error("stack action stopped at %v: %v", e.PR, e.Err)
		reason = locale.TrString("damast.stack.reason_internal")
	}
	return locale.TrString("damast.stack.stopped_at", e.PR.Index, reason)
}

// IsUserAllowedToRebase reports whether doer may force-push a rebased head to every layer,
// using the same rules as Forgejo's "Update branch by rebase" (write access, branch
// protection, force-push and the repository's "allow rebase update" setting).
func (s *Stack) IsUserAllowedToRebase(ctx context.Context, doer *user_model.User) (bool, error) {
	if doer == nil || !s.IsStacked() {
		return false, nil
	}
	for _, l := range s.Layers {
		if err := l.PR.LoadBaseRepo(ctx); err != nil {
			return false, err
		}
		if err := l.PR.LoadHeadRepo(ctx); err != nil {
			return false, err
		}
		perm, err := access_model.GetUserRepoPermission(ctx, l.PR.BaseRepo, doer)
		if err != nil {
			return false, err
		}
		mergeAllowed, rebaseAllowed, err := pull_service.IsUserAllowedToUpdate(ctx, l.PR, doer, perm, perm)
		if err != nil {
			return false, err
		}
		if !mergeAllowed || !rebaseAllowed {
			return false, nil
		}
	}
	return true, nil
}

// reload fetches the current state of a layer and checks that it is still where the
// caller saw it: open, same base, head at the expected commit.
func reload(ctx context.Context, l *Layer, heads map[int64]string, bases ...string) (*issues_model.PullRequest, string, error) {
	want := heads[l.PR.Index]
	pr, err := issues_model.GetPullRequestByID(ctx, l.PR.ID)
	if err != nil {
		return l.PR, "", err
	}
	if err := pr.LoadIssue(ctx); err != nil {
		return nil, "", err
	}
	if err := pr.LoadBaseRepo(ctx); err != nil {
		return nil, "", err
	}
	if err := pr.LoadHeadRepo(ctx); err != nil {
		return nil, "", err
	}
	pr.Issue.Repo = pr.BaseRepo
	if want == "" || pr.HasMerged || pr.Issue.IsClosed {
		return pr, "", ErrStale
	}
	for _, b := range bases {
		if pr.BaseBranch == b {
			return pr, want, nil
		}
	}
	return pr, "", ErrStale
}

// Rebase rebases every layer, bottom first, onto the current head of its parent (the
// bottom layer onto the trunk). heads maps PR index to the head commit the caller saw;
// a layer whose branch moved stops the run. Layers that are already up to date are
// skipped. On error the layers before the failing one stay rebased, later ones untouched.
func Rebase(ctx context.Context, s *Stack, doer *user_model.User, heads map[int64]string) (rebased []*issues_model.PullRequest, err error) {
	if ok, err := s.IsUserAllowedToRebase(ctx, doer); err != nil {
		return nil, err
	} else if !ok {
		return nil, &LayerError{PR: s.Current.PR, Err: ErrNotAllowed}
	}
	for _, l := range s.Layers {
		base := s.Trunk
		if l.Parent != nil {
			base = l.Parent.PR.HeadBranch
		}
		pr, want, err := reload(ctx, l, heads, base)
		if err != nil {
			return rebased, &LayerError{PR: pr, Err: err}
		}
		err = pull_service.DamastRebaseUpdate(ctx, pr, doer, want)
		if errors.Is(err, pull_service.ErrPullRequestIsUpdateToDate) {
			continue
		} else if err != nil {
			return rebased, &LayerError{PR: pr, Err: err}
		}
		rebased = append(rebased, pr)
	}
	return rebased, nil
}

// MergeUpTo merges the layers from the bottom up to and including target, each through
// Forgejo's normal merge checks (permissions, approvals, required checks, draft, conflicts,
// dependencies, signing) and merge path. Each layer is retargeted to the trunk before it is
// merged. heads maps PR index to the head commit the caller saw. On error the layers
// before the failing one stay merged.
func MergeUpTo(ctx context.Context, s *Stack, target *Layer, doer *user_model.User, style repo_model.MergeStyle, heads map[int64]string) (merged []*issues_model.PullRequest, err error) {
	if err := s.Root.PR.LoadBaseRepo(ctx); err != nil {
		return nil, err
	}
	if style == "" {
		prUnit, err := s.Root.PR.BaseRepo.GetUnit(ctx, unit.TypePullRequests)
		if err != nil {
			return nil, err
		}
		style = prUnit.PullRequestsConfig().GetDefaultMergeStyle()
	}
	gitRepo, closer, err := gitrepo.RepositoryFromContextOrOpen(ctx, s.Root.PR.BaseRepo)
	if err != nil {
		return nil, err
	}
	defer closer.Close()

	var prev *issues_model.PullRequest
	for _, l := range s.Path(target) {
		bases := []string{s.Trunk}
		if prev != nil {
			bases = append(bases, prev.HeadBranch)
		}
		pr, want, err := reload(ctx, l, heads, bases...)
		if err != nil {
			return merged, &LayerError{PR: pr, Err: err}
		}
		perm, err := access_model.GetUserRepoPermission(ctx, pr.BaseRepo, doer)
		if err != nil {
			return merged, err
		}
		if pr.BaseBranch != s.Trunk {
			// the merge notifier normally did this already; only someone who may merge may retarget
			if ok, err := pull_service.IsUserAllowedToMerge(ctx, pr, perm, doer); err != nil || !ok {
				return merged, &LayerError{PR: pr, Err: cmp.Or(err, error(pull_service.ErrUserNotAllowedToMerge))}
			}
			if err := pull_service.ChangeTargetBranch(ctx, pr, doer, s.Trunk); err != nil {
				return merged, &LayerError{PR: pr, Err: err}
			}
		}
		if pr.IsChecking() {
			pull_service.DamastTestNow(ctx, pr.ID)
			if pr, want, err = reload(ctx, l, heads, s.Trunk); err != nil {
				return merged, &LayerError{PR: pr, Err: err}
			}
		}
		if err := pull_service.CheckPullMergeable(ctx, doer, &perm, pr, pull_service.MergeCheckTypeGeneral, false, style); err != nil {
			return merged, &LayerError{PR: pr, Err: err}
		}
		message, body, err := pull_service.GetDefaultMergeMessage(ctx, gitRepo, pr, style)
		if err != nil {
			return merged, &LayerError{PR: pr, Err: err}
		}
		if body != "" {
			message += "\n\n" + body
		}
		if err := pull_service.Merge(ctx, pr, doer, gitRepo, style, want, message, false); err != nil {
			return merged, &LayerError{PR: pr, Err: err}
		}
		merged = append(merged, pr)
		prev = pr
	}
	return merged, nil
}

// Init registers the notifier that retargets children of a merged layer.
func Init() error {
	notify_service.RegisterNotifier(&notifier{})
	return nil
}

type notifier struct {
	notify_service.NullNotifier
}

func (*notifier) MergePullRequest(ctx context.Context, doer *user_model.User, pr *issues_model.PullRequest) {
	RetargetChildren(ctx, doer, pr)
}

func (*notifier) AutoMergePullRequest(ctx context.Context, doer *user_model.User, pr *issues_model.PullRequest) {
	RetargetChildren(ctx, doer, pr)
}

// RetargetChildren moves the PRs based on a merged PR's head branch to its base branch,
// also when the head branch is kept. Upstream Forgejo only does this when the branch is
// deleted; Damast follows the same switch ([repository.pull-request] RETARGET_CHILDREN_ON_MERGE).
// Long-lived branches (the default branch or any protected branch) are left alone, so that
// merging e.g. develop into main does not move every PR aimed at develop.
func RetargetChildren(ctx context.Context, doer *user_model.User, pr *issues_model.PullRequest) {
	if !setting.Repository.PullRequest.RetargetChildrenOnMerge || pr.HeadRepoID != pr.BaseRepoID || pr.Flow != issues_model.PullRequestFlowGithub {
		return
	}
	if err := pr.LoadBaseRepo(ctx); err != nil {
		log.Error("RetargetChildren: %v", err)
		return
	}
	if pr.HeadBranch == pr.BaseRepo.DefaultBranch {
		return
	}
	if pb, err := git_model.GetFirstMatchProtectedBranchRule(ctx, pr.BaseRepoID, pr.HeadBranch); err != nil || pb != nil {
		return
	}
	if err := pull_service.RetargetBranchPulls(ctx, doer, pr.HeadRepoID, pr.HeadBranch, pr.BaseBranch); err != nil {
		log.Error("RetargetChildren of %v: %v", pr, err)
	}
}
