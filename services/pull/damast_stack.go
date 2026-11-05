// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// DAMAST: stacked pull requests (services/damast/stack). Lives in package pull only
// because it reuses the unexported merge machinery.

package pull

import (
	"context"
	"fmt"
	"strings"

	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	repo_module "forgejo.org/modules/repository"
	"forgejo.org/modules/setting"
)

// DamastRebaseUpdate rebases the head branch of pr onto its current base branch, like
// "Update branch by rebase", but as a compare-and-swap: it fails with ErrSHADoesNotMatch
// or ErrPushOutOfDate when the head branch is not at expectedHead. Returns
// ErrPullRequestIsUpdateToDate when there is nothing to do.
func DamastRebaseUpdate(ctx context.Context, pr *issues_model.PullRequest, doer *user_model.User, expectedHead string) error {
	if pr.Flow != issues_model.PullRequestFlowGithub || pr.HeadRepoID != pr.BaseRepoID {
		return fmt.Errorf("stack rebase works only on same-repo branch pull requests")
	}
	if expectedHead == "" {
		return fmt.Errorf("missing expected head commit for %v", pr)
	}

	defer pullWorkingPool.Lock(fmt.Sprint(pr.ID))()

	diff, err := GetDiverging(ctx, pr)
	if err != nil {
		return err
	} else if diff.Behind == 0 {
		return ErrPullRequestIsUpdateToDate
	}
	pr.CommitsBehind = diff.Behind // rebaseTrackingOnToBase skips the rebase when this is 0

	defer func() {
		AddTestPullRequestTask(ctx, doer, pr.BaseRepo.ID, pr.BaseBranch, false, "", "", 0)
	}()

	mergeCtx, cancel, err := createTemporaryRepoForMerge(ctx, pr, doer, expectedHead)
	if err != nil {
		return err
	}
	defer cancel()

	oldMergeBase, _, _ := git.NewCommand(ctx, "merge-base").AddDashesAndList(baseBranch, trackingBranch).RunStdString(&git.RunOpts{Dir: mergeCtx.tmpBasePath})
	oldMergeBase = strings.TrimSpace(oldMergeBase)

	if err := rebaseTrackingOnToBase(mergeCtx, repo_model.MergeStyleRebaseUpdate); err != nil {
		return err
	}

	if setting.LFS.StartServer {
		if err := LFSPush(ctx, mergeCtx.tmpBasePath, baseBranch, oldMergeBase, &issues_model.PullRequest{
			HeadRepoID: pr.BaseRepoID,
			BaseRepoID: pr.HeadRepoID,
		}); err != nil {
			return err
		}
	}

	if err := pr.HeadRepo.LoadOwner(ctx); err != nil {
		return err
	}

	// --force-with-lease makes the push itself the compare-and-swap
	pushCmd := git.NewCommand(ctx, "push").
		AddOptionFormat("--force-with-lease=%s:%s", git.BranchPrefix+pr.HeadBranch, expectedHead).
		AddDynamicArguments("head_repo", stagingBranch+":"+git.BranchPrefix+pr.HeadBranch)
	mergeCtx.outbuf.Reset()
	mergeCtx.errbuf.Reset()
	if err := pushCmd.Run(&git.RunOpts{
		Env:    repo_module.FullPushingEnvironment(pr.HeadRepo.Owner, doer, pr.HeadRepo, pr.HeadRepo.Name, pr.ID),
		Dir:    mergeCtx.tmpBasePath,
		Stdout: mergeCtx.outbuf,
		Stderr: mergeCtx.errbuf,
	}); err != nil {
		stderr := mergeCtx.errbuf.String()
		switch {
		case strings.Contains(stderr, "stale info"), strings.Contains(stderr, "non-fast-forward"):
			return &git.ErrPushOutOfDate{StdOut: mergeCtx.outbuf.String(), StdErr: stderr, Err: err}
		case strings.Contains(stderr, "! [remote rejected]"):
			rejected := &git.ErrPushRejected{StdOut: mergeCtx.outbuf.String(), StdErr: stderr, Err: err}
			rejected.GenerateMessage()
			return rejected
		}
		return fmt.Errorf("git push: %s", stderr)
	}
	return nil
}

// DamastTestNow runs the conflict check of a pull request synchronously, so that a
// stack merge does not have to wait for the patch checker queue after a retarget.
func DamastTestNow(ctx context.Context, id int64) {
	testPRProtected(ctx, id)
}
