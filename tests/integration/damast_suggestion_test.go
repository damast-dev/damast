// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	auth_model "forgejo.org/models/auth"
	damast_model "forgejo.org/models/damast"
	git_model "forgejo.org/models/git"
	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
	api "forgejo.org/modules/structs"
	routers_repo "forgejo.org/routers/api/v1/repo"
	pull_service "forgejo.org/services/pull"
	repo_service "forgejo.org/services/repository"
	files_service "forgejo.org/services/repository/files"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// damastSuggestionPR creates branch in headRepo with files (on top of master) and a pull request into repo1.
func damastSuggestionPR(t *testing.T, base, head *repo_model.Repository, owner *user_model.User, branch string, files map[string]string) *issues_model.PullRequest {
	t.Helper()
	var changes []*files_service.ChangeRepoFile
	for p, c := range files {
		changes = append(changes, &files_service.ChangeRepoFile{Operation: "create", TreePath: p, ContentReader: strings.NewReader(c)})
	}
	_, err := files_service.ChangeRepoFiles(git.DefaultContext, head, owner, &files_service.ChangeRepoFilesOptions{
		Files: changes, Message: "add files", OldBranch: "master", NewBranch: branch,
	})
	require.NoError(t, err)
	pr := &issues_model.PullRequest{
		HeadRepoID: head.ID, BaseRepoID: base.ID, HeadBranch: branch, BaseBranch: "master",
		HeadRepo: head, BaseRepo: base, Type: issues_model.PullRequestGitea,
	}
	issue := &issues_model.Issue{RepoID: base.ID, Title: branch, PosterID: owner.ID, Poster: owner, IsPull: true}
	require.NoError(t, pull_service.NewPullRequest(git.DefaultContext, base, issue, nil, nil, pr, nil))
	return pr
}

// damastReview submits a review with code comments and returns their IDs in order.
func damastReview(t *testing.T, token string, pr *issues_model.PullRequest, comments ...api.CreatePullReviewComment) []int64 {
	t.Helper()
	url := fmt.Sprintf("/api/v1/repos/user2/repo1/pulls/%d/reviews", pr.Index)
	resp := MakeRequest(t, NewRequestWithJSON(t, "POST", url, &api.CreatePullReviewOptions{
		Event: api.ReviewStateComment, Body: "review", Comments: comments,
	}).AddTokenAuth(token), http.StatusOK)
	var review api.PullReview
	DecodeJSON(t, resp, &review)
	resp = MakeRequest(t, NewRequestf(t, "GET", "%s/%d/comments", url, review.ID).AddTokenAuth(token), http.StatusOK)
	var list []*api.PullReviewComment
	DecodeJSON(t, resp, &list)
	byBody := map[string]int64{}
	for _, c := range list {
		byBody[c.Body] = c.ID
	}
	ids := make([]int64, len(comments))
	for i, c := range comments {
		ids[i] = byBody[c.Body]
		require.NotZero(t, ids[i])
	}
	return ids
}

func damastApply(t *testing.T, token string, pr *issues_model.PullRequest, ids []int64, message string, status int) string {
	t.Helper()
	resp := MakeRequest(t, NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/user2/repo1/pulls/%d/damast/suggestions/apply", pr.Index),
		&routers_repo.DamastApplySuggestionsOption{CommentIDs: ids, Message: message}).AddTokenAuth(token), status)
	if status != http.StatusOK {
		return ""
	}
	var out map[string]string
	DecodeJSON(t, resp, &out)
	return out["commit"]
}

func damastHeadFile(t *testing.T, pr *issues_model.PullRequest, path string) (string, *git.Commit) {
	t.Helper()
	gitRepo, err := gitrepo.OpenRepository(git.DefaultContext, pr.HeadRepo)
	require.NoError(t, err)
	defer gitRepo.Close()
	commit, err := gitRepo.GetBranchCommit(pr.HeadBranch)
	require.NoError(t, err)
	content, _, err := damast_model.ReadFileAt(gitRepo, commit.ID.String(), path)
	require.NoError(t, err)
	return string(content), commit
}

// damastPush commits new content of path to the head branch, like a push by doer.
func damastPush(t *testing.T, pr *issues_model.PullRequest, doer *user_model.User, path, content, message string) {
	t.Helper()
	_, head := damastHeadFile(t, pr, path)
	_, err := files_service.ChangeRepoFiles(git.DefaultContext, pr.HeadRepo, doer, &files_service.ChangeRepoFilesOptions{
		Files:        []*files_service.ChangeRepoFile{{Operation: "update", TreePath: path, ContentReader: strings.NewReader(content)}},
		Message:      message,
		OldBranch:    pr.HeadBranch,
		LastCommitID: head.ID.String(),
	})
	require.NoError(t, err)
}

func suggest(path string, line, extra int64, body string) api.CreatePullReviewComment {
	return api.CreatePullReviewComment{Path: path, NewLineNum: line, ExtraLinesCount: extra, Body: body}
}

func TestDamastSuggestions(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, _ *url.URL) {
		ctx := git.DefaultContext
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		user4 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
		user5 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
		user5.KeepEmailPrivate = true // credited with the noreply address
		require.NoError(t, user_model.UpdateUserCols(ctx, user5, "keep_email_private"))
		repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})

		ownerToken := getUserToken(t, "user2", auth_model.AccessTokenScopeWriteRepository)
		token4 := getUserToken(t, "user4", auth_model.AccessTokenScopeWriteRepository)
		token5 := getUserToken(t, "user5", auth_model.AccessTokenScopeWriteRepository)
		trailer4 := "Co-authored-by: user4 <user4@example.com>"
		trailer5 := "Co-authored-by: User Five <" + user5.GetPlaceholderEmail() + ">"

		t.Run("single", func(t *testing.T) {
			pr := damastSuggestionPR(t, repo1, repo1, user2, "sug-single", map[string]string{"a.txt": "one\ntwo\nthree\n"})
			ids := damastReview(t, token4, pr, suggest("a.txt", 2, 0, "nit\n```suggestion\nTWO\n```"))

			// reviewers see the diff without buttons, the PR author with them
			doc := NewHTMLParser(t, loginUser(t, "user4").MakeRequest(t, NewRequestf(t, "GET", "/user2/repo1/pulls/%d/files", pr.Index), http.StatusOK).Body)
			assert.Equal(t, 1, doc.Find(".damast-suggestion").Length())
			assert.Equal(t, 0, doc.Find(".damast-suggestion[data-apply-url]").Length())
			session := loginUser(t, "user2")
			doc = NewHTMLParser(t, session.MakeRequest(t, NewRequestf(t, "GET", "/user2/repo1/pulls/%d/files", pr.Index), http.StatusOK).Body)
			assert.Equal(t, 1, doc.Find(".damast-suggestion[data-apply-url] .damast-suggestion-commit").Length())
			assert.Equal(t, "two", doc.Find(".damast-suggestion-del code").Text())
			assert.Equal(t, "TWO", doc.Find(".damast-suggestion-add code").Text())
			doc = NewHTMLParser(t, session.MakeRequest(t, NewRequestf(t, "GET", "/user2/repo1/pulls/%d", pr.Index), http.StatusOK).Body)
			assert.Equal(t, 1, doc.Find(".damast-suggestion[data-apply-url]").Length(), "conversation tab")

			// list API
			var list []routers_repo.DamastSuggestion
			DecodeJSON(t, MakeRequest(t, NewRequestf(t, "GET", "/api/v1/repos/user2/repo1/pulls/%d/damast/suggestions", pr.Index).AddTokenAuth(ownerToken), http.StatusOK), &list)
			require.Len(t, list, 1)
			assert.Equal(t, routers_repo.DamastSuggestion{
				CommentID: ids[0], ReviewID: list[0].ReviewID, Author: "user4", Path: "a.txt", StartLine: 2, EndLine: 2,
				Old: []string{"two"}, New: []string{"TWO"}, Applicable: true, HTMLURL: list[0].HTMLURL,
			}, list[0])

			// reader can't apply, neither via API nor web
			damastApply(t, token4, pr, ids, "", http.StatusForbidden)
			loginUser(t, "user4").MakeRequest(t, NewRequestWithValues(t, "POST", fmt.Sprintf("/user2/repo1/pulls/%d/damast/suggestions/apply", pr.Index), map[string]string{
				"comment_ids": fmt.Sprint(ids[0]),
			}), http.StatusForbidden)

			// web route as the owner
			session.MakeRequest(t, NewRequestWithValues(t, "POST", fmt.Sprintf("/user2/repo1/pulls/%d/damast/suggestions/apply", pr.Index), map[string]string{
				"comment_ids": fmt.Sprint(ids[0]),
				"message":     "",
			}), http.StatusOK)
			content, commit := damastHeadFile(t, pr, "a.txt")
			assert.Equal(t, "one\nTWO\nthree\n", content)
			assert.Equal(t, "Apply suggestions from code review\n\n"+trailer4, strings.TrimSpace(commit.CommitMessage))
			assert.Equal(t, user2.GitName(), commit.Author.Name)

			c := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: ids[0]})
			assert.Equal(t, user2.ID, c.ResolveDoerID, "conversation resolved")

			// applying again: the lines changed
			damastApply(t, ownerToken, pr, ids, "", http.StatusConflict)
		})

		t.Run("batch across files with range and delete", func(t *testing.T) {
			pr := damastSuggestionPR(t, repo1, repo1, user2, "sug-batch", map[string]string{
				"a.txt":  "1\n2\n3\n4\n5\n",
				"b.go":   "package b\r\n\r\nfunc X() {}\r\n", // CRLF stays CRLF
				"keep.x": "unchanged\n",
			})
			ids4 := damastReview(t, token4, pr,
				suggest("a.txt", 2, 2, "range\n```suggestion\ntwo-four\n```"),
				suggest("a.txt", 5, 0, "delete\n```suggestion\n```"),
			)
			ids5 := damastReview(t, token5, pr, suggest("b.go", 3, 0, "go\n~~~suggestion\nfunc X() { /* ü */ }\n~~~"))

			// someone pushes above the comments: they follow their lines
			damastPush(t, pr, user2, "a.txt", "0\n1\n2\n3\n4\n5\n", "prepend")

			ids := append(ids4, ids5...)
			damastApply(t, ownerToken, pr, ids, "Polish\n\nSigned-off-by: user2 <user2@example.com>", http.StatusOK)
			a, commit := damastHeadFile(t, pr, "a.txt")
			assert.Equal(t, "0\n1\ntwo-four\n", a)
			b, _ := damastHeadFile(t, pr, "b.go")
			assert.Equal(t, "package b\r\n\r\nfunc X() { /* ü */ }\r\n", b)
			assert.Equal(t, "Polish\n\nSigned-off-by: user2 <user2@example.com>\n"+trailer4+"\n"+trailer5, strings.TrimSpace(commit.CommitMessage))
			parent, err := commit.Parent(0) // all three suggestions in one commit
			require.NoError(t, err)
			assert.Equal(t, "prepend", strings.TrimSpace(parent.CommitMessage))
			for _, id := range ids {
				c := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: id})
				assert.Equal(t, user2.ID, c.ResolveDoerID)
			}
		})

		t.Run("outdated", func(t *testing.T) {
			pr := damastSuggestionPR(t, repo1, repo1, user2, "sug-outdated", map[string]string{"a.txt": "a\nb\n"})
			ids := damastReview(t, token4, pr, suggest("a.txt", 2, 0, "x\n```suggestion\nB\n```"))
			damastPush(t, pr, user2, "a.txt", "a\nbee\n", "change")
			damastApply(t, ownerToken, pr, ids, "", http.StatusConflict)
		})

		t.Run("overlap", func(t *testing.T) {
			pr := damastSuggestionPR(t, repo1, repo1, user2, "sug-overlap", map[string]string{"a.txt": "a\nb\nc\n"})
			ids := damastReview(t, token4, pr,
				suggest("a.txt", 1, 1, "first\n```suggestion\nAB\n```"),
				suggest("a.txt", 2, 0, "second\n```suggestion\nB\n```"),
			)
			damastApply(t, ownerToken, pr, ids, "", http.StatusConflict)
			content, _ := damastHeadFile(t, pr, "a.txt")
			assert.Equal(t, "a\nb\nc\n", content)
			// one at a time works
			damastApply(t, ownerToken, pr, ids[:1], "", http.StatusOK)
		})

		t.Run("not a suggestion", func(t *testing.T) {
			pr := damastSuggestionPR(t, repo1, repo1, user2, "sug-invalid", map[string]string{"a.txt": "a\n"})
			ids := damastReview(t, token4, pr,
				suggest("a.txt", 1, 0, "plain comment"),
				suggest("a.txt", 1, 0, "two\n```suggestion\nx\n```\n```suggestion\ny\n```"),
			)
			damastApply(t, ownerToken, pr, ids[:1], "", http.StatusUnprocessableEntity)
			damastApply(t, ownerToken, pr, ids[1:], "", http.StatusUnprocessableEntity)
		})

		t.Run("protected branch", func(t *testing.T) {
			pr := damastSuggestionPR(t, repo1, repo1, user2, "sug-protected", map[string]string{"a.txt": "a\n"})
			ids := damastReview(t, token4, pr, suggest("a.txt", 1, 0, "x\n```suggestion\nA\n```"))
			require.NoError(t, git_model.UpdateProtectBranch(ctx, repo1, &git_model.ProtectedBranch{RepoID: repo1.ID, RuleName: "sug-protected"}, git_model.WhitelistOptions{}))
			damastApply(t, ownerToken, pr, ids, "", http.StatusForbidden)
		})

		t.Run("closed", func(t *testing.T) {
			pr := damastSuggestionPR(t, repo1, repo1, user2, "sug-closed", map[string]string{"a.txt": "a\n"})
			ids := damastReview(t, token4, pr, suggest("a.txt", 1, 0, "x\n```suggestion\nA\n```"))
			closed := "closed"
			MakeRequest(t, NewRequestWithJSON(t, "PATCH", fmt.Sprintf("/api/v1/repos/user2/repo1/pulls/%d", pr.Index), &api.EditPullRequestOption{State: &closed}).AddTokenAuth(ownerToken), http.StatusCreated)
			damastApply(t, ownerToken, pr, ids, "", http.StatusConflict)
		})

		t.Run("fork", func(t *testing.T) {
			fork, err := repo_service.ForkRepositoryAndUpdates(ctx, user4, user4, repo_service.ForkRepoOptions{BaseRepo: repo1, Name: "repo1-sug"})
			require.NoError(t, err)
			pr := damastSuggestionPR(t, repo1, fork, user4, "sug-fork", map[string]string{"a.txt": "a\nb\n"})
			ids := damastReview(t, token5, pr, suggest("a.txt", 2, 0, "x\n```suggestion\nB\n```"))

			// base maintainer without "allow edits by maintainers"
			damastApply(t, ownerToken, pr, ids, "", http.StatusForbidden)

			require.NoError(t, pull_service.SetAllowEdits(ctx, user4, pr, true))
			damastApply(t, ownerToken, pr, ids, "", http.StatusOK)
			content, commit := damastHeadFile(t, pr, "a.txt")
			assert.Equal(t, "a\nB\n", content)
			assert.Equal(t, "Apply suggestions from code review\n\n"+trailer5, strings.TrimSpace(commit.CommitMessage))
		})

		t.Run("pending review", func(t *testing.T) {
			pr := damastSuggestionPR(t, repo1, repo1, user2, "sug-pending", map[string]string{"a.txt": "a\n"})
			url := fmt.Sprintf("/api/v1/repos/user2/repo1/pulls/%d/reviews", pr.Index)
			resp := MakeRequest(t, NewRequestWithJSON(t, "POST", url, &api.CreatePullReviewOptions{
				Event: api.ReviewStatePending, Body: "pending", Comments: []api.CreatePullReviewComment{suggest("a.txt", 1, 0, "```suggestion\nA\n```")},
			}).AddTokenAuth(token4), http.StatusOK)
			var review api.PullReview
			DecodeJSON(t, resp, &review)
			c := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ReviewID: review.ID, Type: issues_model.CommentTypeCode})
			damastApply(t, ownerToken, pr, []int64{c.ID}, "", http.StatusUnprocessableEntity)
		})
	})
}
