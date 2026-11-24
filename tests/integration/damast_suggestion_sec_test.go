// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	actions_model "forgejo.org/models/actions"
	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	git_model "forgejo.org/models/git"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/lfs"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/test"
	routers_repo "forgejo.org/routers/api/v1/repo"
	pull_service "forgejo.org/services/pull"
	repo_service "forgejo.org/services/repository"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Security review of suggested changes: who may write into the head branch, and what.
func TestDamastSuggestionsSecurity(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, _ *url.URL) {
		ctx := git.DefaultContext
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		user4 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
		repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
		ownerToken := getUserToken(t, "user2", auth_model.AccessTokenScopeWriteRepository)
		token4 := getUserToken(t, "user4", auth_model.AccessTokenScopeWriteRepository)
		token5 := getUserToken(t, "user5", auth_model.AccessTokenScopeWriteRepository)

		applyRaw := func(t *testing.T, token string, pr *issues_model.PullRequest, body map[string]any, status int) {
			t.Helper()
			MakeRequest(t, NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/user2/repo1/pulls/%d/damast/suggestions/apply", pr.Index), body).AddTokenAuth(token), status)
		}
		// user4 can fork repo1 only once; the subtests use branches of that fork.
		require.NoError(t, repo1.LoadOwner(ctx))
		sharedFork, err := repo_service.ForkRepositoryAndUpdates(ctx, user4, user4, repo_service.ForkRepoOptions{BaseRepo: repo1, Name: "repo1-sec"})
		require.NoError(t, err)
		fork := func(t *testing.T, _ string) *repo_model.Repository { return sharedFork }

		// A reviewer may edit the suggestion between page load and click; a request without the
		// versions the page showed would commit content nobody has seen.
		t.Run("web requires content_versions", func(t *testing.T) {
			pr := damastSuggestionPR(t, repo1, repo1, user2, "sec-web-versions", map[string]string{"a.txt": "a\n"})
			ids := damastReview(t, token4, pr, suggest("a.txt", 1, 0, "x\n```suggestion\nA\n```"))
			loginUser(t, "user2").MakeRequest(t, NewRequestWithValues(t, "POST", fmt.Sprintf("/user2/repo1/pulls/%d/damast/suggestions/apply", pr.Index), map[string]string{
				"comment_ids": fmt.Sprint(ids[0]),
			}), http.StatusUnprocessableEntity)
			content, _ := damastHeadFile(t, pr, "a.txt")
			assert.Equal(t, "a\n", content)
		})

		t.Run("api requires content_versions", func(t *testing.T) {
			pr := damastSuggestionPR(t, repo1, repo1, user2, "sec-api-versions", map[string]string{"a.txt": "a\nb\n"})
			ids := damastReview(t, token4, pr,
				suggest("a.txt", 1, 0, "x\n```suggestion\nA\n```"),
				suggest("a.txt", 2, 0, "y\n```suggestion\nB\n```"))
			applyRaw(t, ownerToken, pr, map[string]any{"comment_ids": ids}, http.StatusUnprocessableEntity)
			// one of two missing
			applyRaw(t, ownerToken, pr, map[string]any{"comment_ids": ids, "content_versions": map[string]int{fmt.Sprint(ids[0]): 0}}, http.StatusUnprocessableEntity)
			content, _ := damastHeadFile(t, pr, "a.txt")
			assert.Equal(t, "a\nb\n", content)
			damastApply(t, ownerToken, pr, ids, "", http.StatusOK) // the helper sends them
		})

		t.Run("batch cap", func(t *testing.T) {
			var file strings.Builder
			var comments []api.CreatePullReviewComment
			for i := 1; i <= 101; i++ {
				fmt.Fprintf(&file, "l%d\n", i)
				comments = append(comments, suggest("a.txt", int64(i), 0, fmt.Sprintf("c%d\n```suggestion\nL%d\n```", i, i)))
			}
			pr := damastSuggestionPR(t, repo1, repo1, user2, "sec-cap", map[string]string{"a.txt": file.String()})
			ids := damastReview(t, token4, pr, comments...)
			damastApply(t, ownerToken, pr, ids, "", http.StatusUnprocessableEntity)
			damastApply(t, ownerToken, pr, ids[:100], "", http.StatusOK)
		})

		t.Run("suggestion size cap", func(t *testing.T) {
			pr := damastSuggestionPR(t, repo1, repo1, user2, "sec-size", map[string]string{"a.txt": "a\n"})
			ids := damastReview(t, token4, pr, suggest("a.txt", 1, 0, "big\n```suggestion\n"+strings.Repeat("x", 70*1024)+"\n```"))
			var list []routers_repo.DamastSuggestion
			DecodeJSON(t, MakeRequest(t, NewRequestf(t, "GET", "/api/v1/repos/user2/repo1/pulls/%d/damast/suggestions", pr.Index).AddTokenAuth(ownerToken), http.StatusOK), &list)
			require.Len(t, list, 1)
			assert.Equal(t, "too_large", list[0].Problem)
			assert.False(t, list[0].Applicable)
			damastApply(t, ownerToken, pr, ids, "", http.StatusUnprocessableEntity)
		})

		t.Run("file size cap", func(t *testing.T) {
			pr := damastSuggestionPR(t, repo1, repo1, user2, "sec-file-size", map[string]string{"a.txt": "a\n" + strings.Repeat("filler\n", 100)})
			ids := damastReview(t, token4, pr, suggest("a.txt", 1, 0, "x\n```suggestion\nA\n```"))
			reset := test.MockVariableValue(&setting.UI.MaxDisplayFileSize, int64(100))
			damastApply(t, ownerToken, pr, ids, "", http.StatusUnprocessableEntity)
			reset()
			content, _ := damastHeadFile(t, pr, "a.txt")
			assert.True(t, strings.HasPrefix(content, "a\n"))
		})

		// Rewriting an LFS pointer swaps binary content the applier can't review in the diff.
		t.Run("lfs pointer", func(t *testing.T) {
			pointer := lfs.Pointer{Oid: strings.Repeat("a", 64), Size: 3}.StringContent()
			pr := damastSuggestionPR(t, repo1, repo1, user2, "sec-lfs", map[string]string{"model.bin": pointer})
			ids := damastReview(t, token4, pr, suggest("model.bin", 2, 0, "swap\n```suggestion\noid sha256:"+strings.Repeat("b", 64)+"\n```"))
			damastApply(t, ownerToken, pr, ids, "", http.StatusUnprocessableEntity)
			content, _ := damastHeadFile(t, pr, "model.bin")
			assert.Equal(t, pointer, content)
		})

		// A token restricted to the base repository must not write into the fork behind the PR.
		t.Run("fine-grained token", func(t *testing.T) {
			f := fork(t, "repo1-sec-fg")
			pr := damastSuggestionPR(t, repo1, f, user4, "sec-fg", map[string]string{"a.txt": "a\n"})
			require.NoError(t, pull_service.SetAllowEdits(ctx, user4, pr, true))
			ids := damastReview(t, token5, pr, suggest("a.txt", 1, 0, "x\n```suggestion\nA\n```"))
			scoped := createFineGrainedRepoAccessToken(t, "user2", []auth_model.AccessTokenScope{auth_model.AccessTokenScopeWriteRepository}, []int64{repo1.ID})
			var list []routers_repo.DamastSuggestion
			DecodeJSON(t, MakeRequest(t, NewRequestf(t, "GET", "/api/v1/repos/user2/repo1/pulls/%d/damast/suggestions", pr.Index).AddTokenAuth(scoped), http.StatusOK), &list)
			require.Len(t, list, 1)
			assert.False(t, list[0].Applicable)
			damastApply(t, scoped, pr, ids, "", http.StatusForbidden)
			content, _ := damastHeadFile(t, pr, "a.txt")
			assert.Equal(t, "a\n", content)
			// a token for both repositories may
			both := createFineGrainedRepoAccessToken(t, "user2", []auth_model.AccessTokenScope{auth_model.AccessTokenScopeWriteRepository}, []int64{repo1.ID, f.ID})
			damastApply(t, both, pr, ids, "", http.StatusOK)
		})

		// Actions tokens: write on the base repository (push, pull_request_target) or read (fork PR).
		// None of them may commit suggestions.
		t.Run("actions token", func(t *testing.T) {
			same := damastSuggestionPR(t, repo1, repo1, user2, "sec-actions", map[string]string{"a.txt": "a\n"})
			sameIDs := damastReview(t, token4, same, suggest("a.txt", 1, 0, "x\n```suggestion\nA\n```"))
			f := fork(t, "repo1-sec-actions")
			forked := damastSuggestionPR(t, repo1, f, user4, "sec-actions-fork", map[string]string{"a.txt": "a\n"})
			require.NoError(t, pull_service.SetAllowEdits(ctx, user4, forked, true))
			forkIDs := damastReview(t, token5, forked, suggest("a.txt", 1, 0, "x\n```suggestion\nA\n```"))

			task := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: 47})
			task.RepoID = repo1.ID
			task.OwnerID = repo1.OwnerID
			for _, isFork := range []bool{false, true} {
				task.IsForkPullRequest = isFork
				task.GenerateToken()
				require.NoError(t, actions_model.UpdateTask(ctx, task))
				damastApply(t, task.Token, same, sameIDs, "", http.StatusForbidden)
				damastApply(t, task.Token, forked, forkIDs, "", http.StatusForbidden)
			}
			content, _ := damastHeadFile(t, same, "a.txt")
			assert.Equal(t, "a\n", content)
			content, _ = damastHeadFile(t, forked, "a.txt")
			assert.Equal(t, "a\n", content)
		})

		// AGit heads live in refs/for/..., there is no branch to commit to.
		t.Run("agit", func(t *testing.T) {
			pr := damastSuggestionPR(t, repo1, repo1, user2, "sec-agit", map[string]string{"a.txt": "a\n"})
			ids := damastReview(t, token4, pr, suggest("a.txt", 1, 0, "x\n```suggestion\nA\n```"))
			pr.Flow = issues_model.PullRequestFlowAGit
			_, err := db.GetEngine(ctx).ID(pr.ID).Cols("flow").Update(pr)
			require.NoError(t, err)
			damastApply(t, ownerToken, pr, ids, "", http.StatusForbidden)
		})

		// Maintainer edits are checked when applying, not when the PR was opened.
		t.Run("maintainer lost access", func(t *testing.T) {
			collab := "/api/v1/repos/user2/repo1/collaborators/user5"
			write := "write"
			MakeRequest(t, NewRequestWithJSON(t, "PUT", collab, &api.AddCollaboratorOption{Permission: &write}).AddTokenAuth(ownerToken), http.StatusNoContent)
			f := fork(t, "repo1-sec-lost")
			pr := damastSuggestionPR(t, repo1, f, user4, "sec-lost", map[string]string{"a.txt": "a\nb\n"})
			require.NoError(t, pull_service.SetAllowEdits(ctx, user4, pr, true))
			ids := damastReview(t, token4, pr,
				suggest("a.txt", 1, 0, "x\n```suggestion\nA\n```"),
				suggest("a.txt", 2, 0, "y\n```suggestion\nB\n```"))
			damastApply(t, token5, pr, ids[:1], "", http.StatusOK)
			MakeRequest(t, NewRequest(t, "DELETE", collab).AddTokenAuth(ownerToken), http.StatusNoContent)
			damastApply(t, token5, pr, ids[1:], "", http.StatusForbidden)
		})

		// Last fork subtest: it makes the fork private.
		t.Run("public-only token into private fork", func(t *testing.T) {
			f := fork(t, "repo1-sec-po")
			pr := damastSuggestionPR(t, repo1, f, user4, "sec-po", map[string]string{"a.txt": "a\n"})
			require.NoError(t, pull_service.SetAllowEdits(ctx, user4, pr, true))
			ids := damastReview(t, token5, pr, suggest("a.txt", 1, 0, "x\n```suggestion\nA\n```"))
			f.IsPrivate = true
			require.NoError(t, repo_model.UpdateRepositoryCols(ctx, f, "is_private"))
			publicOnly := getUserToken(t, "user2", auth_model.AccessTokenScopePublicOnly, auth_model.AccessTokenScopeWriteRepository)
			damastApply(t, publicOnly, pr, ids, "", http.StatusForbidden)
			damastApply(t, ownerToken, pr, ids, "", http.StatusOK)
		})

		t.Run("head branch rules", func(t *testing.T) {
			rule := func(t *testing.T, branch string, pb git_model.ProtectedBranch, wl git_model.WhitelistOptions) []int64 {
				t.Helper()
				pr := damastSuggestionPR(t, repo1, repo1, user2, branch, map[string]string{"a.txt": "a\n"})
				ids := damastReview(t, token4, pr, suggest("a.txt", 1, 0, "x\n```suggestion\nA\n```"))
				pb.RepoID, pb.RuleName = repo1.ID, branch
				require.NoError(t, git_model.UpdateProtectBranch(ctx, repo1, &pb, wl))
				damastApply(t, ownerToken, pr, ids, "", http.StatusForbidden)
				content, _ := damastHeadFile(t, pr, "a.txt")
				assert.Equal(t, "a\n", content)
				return ids
			}
			t.Run("push allowlist without the applier", func(t *testing.T) {
				rule(t, "sec-allowlist", git_model.ProtectedBranch{CanPush: true, EnableWhitelist: true}, git_model.WhitelistOptions{UserIDs: []int64{5}})
			})
			t.Run("protected file pattern", func(t *testing.T) {
				rule(t, "sec-files", git_model.ProtectedBranch{CanPush: true, EnableWhitelist: true, ProtectedFilePatterns: "*.txt"}, git_model.WhitelistOptions{UserIDs: []int64{2}})
			})
			t.Run("require signed commits", func(t *testing.T) {
				defer test.MockVariableValue(&setting.Repository.Signing.CRUDActions, []string{"never"})()
				rule(t, "sec-signed", git_model.ProtectedBranch{CanPush: true, EnableWhitelist: true, RequireSignedCommits: true}, git_model.WhitelistOptions{UserIDs: []int64{2}})
			})
		})

		// Applying is a push: the PR is synchronized (push comment, the same path that sends
		// webhooks and triggers Actions) and stale approvals are dismissed.
		t.Run("dismiss stale approvals", func(t *testing.T) {
			pr := damastSuggestionPR(t, repo1, repo1, user2, "sec-stale", map[string]string{"a.txt": "a\n"})
			ids := damastReview(t, token4, pr, suggest("a.txt", 1, 0, "x\n```suggestion\nA\n```"))
			require.NoError(t, git_model.UpdateProtectBranch(ctx, repo1, &git_model.ProtectedBranch{RepoID: repo1.ID, RuleName: "master", DismissStaleApprovals: true}, git_model.WhitelistOptions{}))
			resp := MakeRequest(t, NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/user2/repo1/pulls/%d/reviews", pr.Index), &api.CreatePullReviewOptions{
				Event: api.ReviewStateApproved, Body: "lgtm",
			}).AddTokenAuth(token5), http.StatusOK)
			var approval api.PullReview
			DecodeJSON(t, resp, &approval)

			damastApply(t, ownerToken, pr, ids, "", http.StatusOK)
			assert.Eventually(t, func() bool {
				r := unittest.AssertExistsAndLoadBean(t, &issues_model.Review{ID: approval.ID})
				return r.Dismissed
			}, 30*time.Second, 100*time.Millisecond, "approval dismissed by the new commit")
			assert.Eventually(t, func() bool {
				return unittest.GetCount(t, &issues_model.Comment{IssueID: pr.IssueID, Type: issues_model.CommentTypePullRequestPush}) > 0
			}, 30*time.Second, 100*time.Millisecond, "pull request synchronized like after a push")
		})
	})
}
