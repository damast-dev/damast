// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package e2e

import (
	"strings"
	"testing"
	"time"

	git_model "forgejo.org/models/git"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	unit_model "forgejo.org/models/unit"
	user_model "forgejo.org/models/user"
	api "forgejo.org/modules/structs"
	pull_service "forgejo.org/services/pull"
	"forgejo.org/services/repository/commitstatus"
	files_service "forgejo.org/services/repository/files"

	"code.forgejo.org/xorm/xorm/convert"
	"github.com/stretchr/testify/require"
)

// damastDeclareRepos declares the repositories of Damast's E2E tests.
func damastDeclareRepos(t *testing.T) {
	// user2/stacked-prs: main <- api <- service <- ui, service <- docs; main has moved on since
	newRepo(t, 2, "stacked-prs", map[unit_model.Type]convert.Conversion{unit_model.TypeCode: &repo_model.UnitConfig{}, unit_model.TypePullRequests: &repo_model.PullRequestsConfig{
		AllowMerge: true, AllowRebase: true, AllowRebaseMerge: true, AllowSquash: true, AllowRebaseUpdate: true, DefaultMergeStyle: repo_model.MergeStyleMerge,
	}}, nil, func(user *user_model.User, repo *repo_model.Repository) {
		commit := func(oldBranch, newBranch, path string) string {
			id := &files_service.IdentityOptions{Name: user.Name, Email: user.Email}
			resp, err := files_service.ChangeRepoFiles(t.Context(), repo, user, &files_service.ChangeRepoFilesOptions{
				Files:     []*files_service.ChangeRepoFile{{Operation: "create", TreePath: path, ContentReader: strings.NewReader(path)}},
				Message:   "Add " + path,
				OldBranch: oldBranch, NewBranch: newBranch, Author: id, Committer: id,
				Dates: &files_service.CommitDateOptions{Author: time.Now(), Committer: time.Now()},
			})
			require.NoError(t, err)
			return resp.Commit.SHA
		}
		layer := func(head, base, title string, state api.CommitStatusState) {
			sha := commit(base, head, head+".md")
			issue := &issues_model.Issue{RepoID: repo.ID, Title: title, PosterID: user.ID, Poster: user, IsPull: true}
			pr := &issues_model.PullRequest{
				HeadRepoID: repo.ID, BaseRepoID: repo.ID, HeadBranch: head, BaseBranch: base,
				HeadRepo: repo, BaseRepo: repo, Type: issues_model.PullRequestGitea,
			}
			require.NoError(t, pull_service.NewPullRequest(t.Context(), repo, issue, nil, nil, pr, nil))
			if state != "" {
				require.NoError(t, commitstatus.CreateCommitStatus(t.Context(), repo, user, sha, &git_model.CommitStatus{State: state, Context: "ci"}))
			}
		}
		layer("api", "main", "Add the stack API", api.CommitStatusSuccess)
		layer("service", "api", "Rebase and merge whole stacks", api.CommitStatusSuccess)
		layer("ui", "service", "Stack map in the pull request view", api.CommitStatusPending)
		layer("docs", "service", "WIP: Document stacked pull requests", "")
		commit("main", "main", "CHANGELOG.md")
	})
}
