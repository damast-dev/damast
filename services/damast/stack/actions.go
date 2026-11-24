// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package stack

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"

	"forgejo.org/models"
	damast_model "forgejo.org/models/damast"
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

// ErrTruncated means the stack has too many pull requests or is circular, so no action is offered.
var ErrTruncated = errors.New("the stack is too large or circular")

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
	case errors.Is(e.Err, ErrTruncated):
		reason = locale.TrString("damast.stack.reason_truncated", maxLayers)
	case errors.Is(e.Err, pull_service.ErrDamastLongLivedBranch):
		reason = locale.TrString("damast.stack.reason_protected")
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
	if doer == nil || s.Truncated {
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
	if slices.Contains(bases, pr.BaseBranch) {
		return pr, want, nil
	}
	return pr, "", ErrStale
}

// Rebase rebases every layer, bottom first, onto the current head of its parent (the
// bottom layer onto the trunk). Each layer is rebased with `--onto`, from the parent head it
// was based on, so a rewritten parent does not replay its old commits. heads maps PR index to
// the head commit the caller saw; a layer whose branch moved stops the run. Layers that are
// already up to date are skipped. On error the layers before the failing one stay rebased,
// later ones untouched. A stack that is Truncated is refused. A lone layer is only rebased
// when its parent was merged (its old parent head is on record).
func Rebase(ctx context.Context, s *Stack, doer *user_model.User, heads map[int64]string) (rebased []*issues_model.PullRequest, err error) {
	if s.Truncated {
		return nil, &LayerError{PR: s.Current.PR, Err: ErrTruncated}
	}
	rootBase, err := damast_model.GetStackBase(ctx, s.Root.PR.ID)
	if err != nil {
		return nil, err
	}
	if !s.IsStacked() && rootBase == "" {
		return nil, &LayerError{PR: s.Current.PR, Err: ErrNotAllowed}
	}
	if ok, err := s.IsUserAllowedToRebase(ctx, doer); err != nil {
		return nil, err
	} else if !ok {
		return nil, &LayerError{PR: s.Current.PR, Err: ErrNotAllowed}
	}
	if err := s.Root.PR.LoadBaseRepo(ctx); err != nil {
		return nil, err
	}
	gitRepo, closer, err := gitrepo.RepositoryFromContextOrOpen(ctx, s.Root.PR.BaseRepo)
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	for _, l := range s.Layers {
		base, oldParentHead := s.Trunk, rootBase
		if l.Parent != nil {
			base, oldParentHead = l.Parent.PR.HeadBranch, heads[l.Parent.PR.Index]
		}
		pr, want, err := reload(ctx, l, heads, base)
		if err != nil {
			return rebased, &LayerError{PR: pr, Err: err}
		}
		err = pull_service.DamastRebaseUpdate(ctx, pr, doer, want, oldParentHead)
		if err != nil && !errors.Is(err, pull_service.ErrPullRequestIsUpdateToDate) {
			return rebased, &LayerError{PR: pr, Err: err}
		}
		if l.Parent != nil { // remember what this layer now sits on
			if head, err := gitRepo.GetBranchCommitID(l.Parent.PR.HeadBranch); err == nil {
				if err := damast_model.SetStackBase(ctx, pr.ID, head); err != nil {
					log.Error("stack base of %v: %v", pr, err)
				}
			}
		}
		if err == nil {
			rebased = append(rebased, pr)
		}
	}
	return rebased, nil
}

// MergeUpTo merges the layers from the bottom up to and including target, each through
// Forgejo's normal merge checks (permissions, approvals, required checks, draft, conflicts,
// dependencies, signing) and merge path. Each layer is retargeted to the trunk before it is
// merged. heads maps PR index to the head commit the caller saw. On error the layers
// before the failing one stay merged.
func MergeUpTo(ctx context.Context, s *Stack, target *Layer, doer *user_model.User, style repo_model.MergeStyle, heads map[int64]string) (merged []*issues_model.PullRequest, err error) {
	if s.Truncated {
		return nil, &LayerError{PR: s.Current.PR, Err: ErrTruncated}
	}
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
		if longLived, err := pull_service.DamastIsLongLivedBranch(ctx, pr.BaseRepo, pr.HeadBranch); err != nil || longLived {
			return merged, &LayerError{PR: pr, Err: cmp.Or(err, pull_service.ErrDamastLongLivedBranch)}
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

// Init registers the notifier that keeps stacks consistent when branches are merged or deleted.
func Init() error {
	notify_service.RegisterNotifier(&notifier{})
	return nil
}

type notifier struct {
	notify_service.NullNotifier
}

func (*notifier) DeleteIssue(ctx context.Context, _ *user_model.User, issue *issues_model.Issue) {
	if issue.IsPull && issue.PullRequest != nil {
		if err := damast_model.DeleteStackBase(ctx, issue.PullRequest.ID); err != nil {
			log.Error("delete stack base of %v: %v", issue, err)
		}
	}
}

func (*notifier) MergePullRequest(ctx context.Context, _ *user_model.User, pr *issues_model.PullRequest) {
	recordChildBases(ctx, pr)
}

func (*notifier) AutoMergePullRequest(ctx context.Context, _ *user_model.User, pr *issues_model.PullRequest) {
	recordChildBases(ctx, pr)
}

// recordChildBases remembers the head the merged pr had, for the open PRs that are based on it.
// Whatever happens to the branch afterwards (squash, delete, retarget), a later rebase of such a
// PR can replay only its own commits (`--onto`).
func recordChildBases(ctx context.Context, pr *issues_model.PullRequest) {
	if pr.HeadRepoID != pr.BaseRepoID || pr.Flow != issues_model.PullRequestFlowGithub {
		return
	}
	if err := pr.LoadBaseRepo(ctx); err != nil {
		log.Error("recordChildBases: %v", err)
		return
	}
	children, err := openSameRepo(ctx, pr.BaseRepoID, "base_branch", pr.HeadBranch, maxLayers)
	if err != nil || len(children) == 0 {
		if err != nil {
			log.Error("recordChildBases of %v: %v", pr, err)
		}
		return
	}
	gitRepo, closer, err := gitrepo.RepositoryFromContextOrOpen(ctx, pr.BaseRepo)
	if err != nil {
		log.Error("recordChildBases of %v: %v", pr, err)
		return
	}
	defer closer.Close()
	head, err := gitRepo.GetBranchCommitID(pr.HeadBranch)
	if err != nil {
		return
	}
	for _, c := range children {
		if err := damast_model.SetStackBase(ctx, c.ID, head); err != nil {
			log.Error("recordChildBases of %v: %v", c, err)
		}
	}
}

// DeleteRef retargets the PRs that were based on a deleted branch whose pull request was merged,
// to that pull request's base branch, like GitHub does. A merge alone never retargets: with the
// branch kept, PRs on top of it stay where they are (a PR into `develop` stays when `develop` is
// merged into `main`). Deleting the branch right after the merge is covered by Forgejo's own
// retargeting; this catches a branch that is deleted later. Follows
// [repository.pull-request] RETARGET_CHILDREN_ON_MERGE.
func (*notifier) DeleteRef(ctx context.Context, doer *user_model.User, repo *repo_model.Repository, refFullName git.RefName) {
	if !setting.Repository.PullRequest.RetargetChildrenOnMerge || !refFullName.IsBranch() {
		return
	}
	branch := refFullName.BranchName()
	pr, err := issues_model.GetLatestPullRequestByHeadInfo(ctx, repo.ID, branch)
	if err != nil {
		log.Error("DeleteRef retarget: %v", err)
		return
	}
	if pr == nil || !pr.HasMerged || pr.BaseRepoID != repo.ID || pr.HeadRepoID != repo.ID || pr.BaseBranch == branch {
		return
	}
	if err := pull_service.RetargetBranchPulls(ctx, doer, repo.ID, branch, pr.BaseBranch); err != nil {
		log.Error("DeleteRef retarget of children of %v: %v", pr, err)
	}
}
