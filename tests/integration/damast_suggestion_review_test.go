// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	auth_model "forgejo.org/models/auth"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	routers_repo "forgejo.org/routers/api/v1/repo"
	issue_service "forgejo.org/services/issue"
	repo_service "forgejo.org/services/repository"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Edge cases found in the adversarial review of suggested changes.
func TestDamastSuggestionsEdgeCases(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, _ *url.URL) {
		ctx := git.DefaultContext
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		user4 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
		repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
		ownerToken := getUserToken(t, "user2", auth_model.AccessTokenScopeWriteRepository)
		token4 := getUserToken(t, "user4", auth_model.AccessTokenScopeWriteRepository)

		// A range whose lines come from different commits: Forgejo anchors the comment at the commit
		// that last changed its first line, where the later lines may still have their old content.
		t.Run("range across commits", func(t *testing.T) {
			pr := damastSuggestionPR(t, repo1, repo1, user2, "sug-range-commits", map[string]string{"a.txt": "a\nb\nc\n"})
			damastPush(t, pr, user2, "a.txt", "a\nB\nc\n", "change b")
			ids := damastReview(t, token4, pr, suggest("a.txt", 1, 1, "r\n```suggestion\nAB\n```"))

			var list []routers_repo.DamastSuggestion
			DecodeJSON(t, MakeRequest(t, NewRequestf(t, "GET", "/api/v1/repos/user2/repo1/pulls/%d/damast/suggestions", pr.Index).AddTokenAuth(ownerToken), http.StatusOK), &list)
			require.Len(t, list, 1)
			assert.Equal(t, []string{"a", "B"}, list[0].Old, "old lines are what the reviewer saw")
			assert.Empty(t, list[0].Problem)

			damastApply(t, ownerToken, pr, ids, "", http.StatusOK)
			content, _ := damastHeadFile(t, pr, "a.txt")
			assert.Equal(t, "AB\nc\n", content)
		})

		// The reviewer edits the suggestion after the maintainer loaded the page: the maintainer must
		// not commit content they haven't seen.
		t.Run("edited after page load", func(t *testing.T) {
			pr := damastSuggestionPR(t, repo1, repo1, user2, "sug-edited", map[string]string{"a.txt": "a\n"})
			ids := damastReview(t, token4, pr, suggest("a.txt", 1, 0, "x\n```suggestion\nA\n```"))
			c := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: ids[0]})
			seen := c.ContentVersion
			old := c.Content
			c.Content = "x\n```suggestion\nrm -rf /\n```"
			require.NoError(t, issue_service.UpdateComment(ctx, c, seen, user4, old))

			session := loginUser(t, "user2")
			link := fmt.Sprintf("/user2/repo1/pulls/%d/damast/suggestions/apply", pr.Index)
			session.MakeRequest(t, NewRequestWithValues(t, "POST", link, map[string]string{
				"comment_ids":      fmt.Sprint(ids[0]),
				"content_versions": fmt.Sprint(seen),
			}), http.StatusConflict)
			content, _ := damastHeadFile(t, pr, "a.txt")
			assert.Equal(t, "a\n", content)

			// with the current version it works
			session.MakeRequest(t, NewRequestWithValues(t, "POST", link, map[string]string{
				"comment_ids":      fmt.Sprint(ids[0]),
				"content_versions": fmt.Sprint(seen + 1),
			}), http.StatusOK)
		})

		t.Run("head branch deleted", func(t *testing.T) {
			pr := damastSuggestionPR(t, repo1, repo1, user2, "sug-deleted", map[string]string{"a.txt": "a\n"})
			ids := damastReview(t, token4, pr, suggest("a.txt", 1, 0, "x\n```suggestion\nA\n```"))
			gitRepo, err := gitrepo.OpenRepository(ctx, repo1)
			require.NoError(t, err)
			require.NoError(t, repo_service.DeleteBranch(ctx, user2, repo1, gitRepo, "sug-deleted"))
			gitRepo.Close()
			damastApply(t, ownerToken, pr, ids, "", http.StatusConflict) // deleting the head branch closes the PR
		})

		t.Run("comment of another pull request", func(t *testing.T) {
			pr1 := damastSuggestionPR(t, repo1, repo1, user2, "sug-idor-1", map[string]string{"a.txt": "a\n"})
			pr2 := damastSuggestionPR(t, repo1, repo1, user2, "sug-idor-2", map[string]string{"a.txt": "a\n"})
			ids := damastReview(t, token4, pr1, suggest("a.txt", 1, 0, "x\n```suggestion\nA\n```"))
			damastApply(t, ownerToken, pr2, ids, "", http.StatusUnprocessableEntity)
		})

		t.Run("token scopes", func(t *testing.T) {
			pr := damastSuggestionPR(t, repo1, repo1, user2, "sug-scope", map[string]string{"a.txt": "a\n"})
			ids := damastReview(t, token4, pr, suggest("a.txt", 1, 0, "x\n```suggestion\nA\n```"))
			damastApply(t, getUserToken(t, "user2", auth_model.AccessTokenScopeReadRepository), pr, ids, "", http.StatusForbidden)
			damastApply(t, getUserToken(t, "user2", auth_model.AccessTokenScopeWriteIssue), pr, ids, "", http.StatusForbidden)
		})
	})
}
