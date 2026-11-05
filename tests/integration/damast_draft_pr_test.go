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
	issues_model "forgejo.org/models/issues"
	pull_model "forgejo.org/models/pull"
	repo_model "forgejo.org/models/repo"
	unit_model "forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/test"
	"forgejo.org/services/automerge"
	"forgejo.org/services/forms"
	files_service "forgejo.org/services/repository/files"
	"forgejo.org/tests/forgery"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// damastDraftBranch creates a branch that changes README.md (owned by user5 in CODEOWNERS).
func damastDraftBranch(t *testing.T, repo *repo_model.Repository, owner *user_model.User, branch string) {
	t.Helper()
	_, err := files_service.ChangeRepoFiles(git.DefaultContext, repo, owner, &files_service.ChangeRepoFilesOptions{
		Files:     []*files_service.ChangeRepoFile{{Operation: "update", TreePath: "README.md", ContentReader: strings.NewReader("changed on " + branch)}},
		OldBranch: "main",
		NewBranch: branch,
		Message:   "change README on " + branch,
	})
	require.NoError(t, err)
}

func damastCodeOwnerRequests(t *testing.T, pr *issues_model.PullRequest) int {
	t.Helper()
	return unittest.GetCount(t, &issues_model.Review{IssueID: pr.IssueID, Type: issues_model.ReviewTypeRequest, ReviewerID: 5})
}

func TestDamastDraftPullRequests(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, _ *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Files: forgery.MapFS{
				"README.md":  forgery.MapFile("hello"),
				"CODEOWNERS": forgery.MapFile("README.md @user5\n"),
			},
		})
		forgery.EnableRepoUnit(t, repo, unit_model.TypePullRequests, nil)
		link := "/" + repo.FullName()

		owner := loginUser(t, "user2")
		reader := loginUser(t, "user4") // can read the public repo, may not edit others' PRs
		ownerToken := getTokenForLoggedInUser(t, owner, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteIssue)

		getPR := func(t *testing.T, branch string) *issues_model.PullRequest {
			t.Helper()
			pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{BaseRepoID: repo.ID, HeadBranch: branch})
			require.NoError(t, pr.LoadIssue(t.Context()))
			return pr
		}
		createDraft := func(t *testing.T, branch, title string) *issues_model.PullRequest {
			t.Helper()
			damastDraftBranch(t, repo, user2, branch)
			owner.MakeRequest(t, NewRequestWithValues(t, "POST", link+"/compare/main..."+branch, map[string]string{
				"title": title,
				"draft": "true",
			}), http.StatusOK)
			return getPR(t, branch)
		}
		setTitle := func(t *testing.T, session *TestSession, pr *issues_model.PullRequest, title string, status int) {
			t.Helper()
			session.MakeRequest(t, NewRequestWithValues(t, "POST", fmt.Sprintf("%s/pulls/%d/title", link, pr.Index), map[string]string{
				"title": title,
			}), status)
		}

		t.Run("compare page offers the draft split button", func(t *testing.T) {
			damastDraftBranch(t, repo, user2, "offer")
			doc := NewHTMLParser(t, owner.MakeRequest(t, NewRequest(t, "GET", link+"/compare/main...offer"), http.StatusOK).Body)
			assert.Equal(t, 1, doc.Find(`#new-issue button[name="draft"][value="true"]`).Length())
			assert.Equal(t, 1, doc.Find(`#new-issue [data-test-name="damast-create-pr"]`).Length())
			// new issue form keeps the plain button
			doc = NewHTMLParser(t, owner.MakeRequest(t, NewRequest(t, "GET", link+"/issues/new"), http.StatusOK).Body)
			assert.Equal(t, 0, doc.Find(`#new-issue button[name="draft"]`).Length())
		})

		pr := createDraft(t, "draft-1", "My feature")

		t.Run("create as draft", func(t *testing.T) {
			assert.Equal(t, "WIP: My feature", pr.Issue.Title)
			assert.True(t, pr.IsWorkInProgress(t.Context()))
			assert.Zero(t, damastCodeOwnerRequests(t, pr), "no code owner review requests while draft")

			// no double prefix
			pr2 := createDraft(t, "draft-2", "[wip] Already marked")
			assert.Equal(t, "[wip] Already marked", pr2.Issue.Title)
		})

		t.Run("merge is blocked", func(t *testing.T) {
			req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/pulls/%d/merge", repo.FullName(), pr.Index), &forms.MergePullRequestForm{Do: "merge"}).AddTokenAuth(ownerToken)
			resp := MakeRequest(t, req, http.StatusMethodNotAllowed)
			assert.Contains(t, resp.Body.String(), "Work in progress PRs cannot be merged")
		})

		t.Run("header, merge box and sidebar", func(t *testing.T) {
			doc := NewHTMLParser(t, owner.MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("%s/pulls/%d", link, pr.Index)), http.StatusOK).Body)
			h1 := strings.TrimSpace(doc.Find("#issue-title-display h1").Text())
			assert.True(t, strings.HasPrefix(h1, "My feature"), h1)
			assert.NotContains(t, h1, "WIP")
			assert.Contains(t, doc.Find(".issue-title-meta .issue-state-label").Text(), "Draft")
			val, _ := doc.Find("#issue-title-editor input").Attr("value")
			assert.Equal(t, "WIP: My feature", val, "the edit box shows the real title")
			assert.Equal(t, 1, doc.Find(`.merge-section [data-test-name="damast-ready-for-review"]`).Length())
			assert.Equal(t, 1, doc.Find(`[data-test-name="damast-sidebar-ready"]`).Length())
			assert.Equal(t, 0, doc.Find(`[data-test-name="damast-convert-to-draft"]`).Length())
		})

		t.Run("reader cannot toggle", func(t *testing.T) {
			doc := NewHTMLParser(t, reader.MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("%s/pulls/%d", link, pr.Index)), http.StatusOK).Body)
			assert.Equal(t, 0, doc.Find(`[data-test-name="damast-ready-for-review"]`).Length())
			assert.Equal(t, 0, doc.Find(`.toggle-wip a`).Length())
			setTitle(t, reader, pr, "My feature", http.StatusForbidden)
			assert.True(t, getPR(t, "draft-1").IsWorkInProgress(t.Context()))
		})

		t.Run("badge in lists", func(t *testing.T) {
			check := func(t *testing.T, path string) {
				t.Helper()
				doc := NewHTMLParser(t, owner.MakeRequest(t, NewRequest(t, "GET", path), http.StatusOK).Body)
				var found bool
				doc.Find("#issue-list .flex-item").Each(func(_ int, item *goquery.Selection) {
					if strings.TrimSpace(item.Find("a.issue-title").Text()) == "My feature" {
						found = true
						assert.Equal(t, 1, item.Find(`[data-test-name="damast-draft-badge"]`).Length(), path)
					}
				})
				assert.True(t, found, "draft PR listed without prefix on %s", path)
			}
			check(t, link+"/pulls")
			check(t, "/pulls?type=created_by")
		})

		t.Run("issues with a WIP title get no badge", func(t *testing.T) {
			req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/issues", repo.FullName()), &api.CreateIssueOption{Title: "WIP: an issue"}).AddTokenAuth(ownerToken)
			MakeRequest(t, req, http.StatusCreated)
			doc := NewHTMLParser(t, owner.MakeRequest(t, NewRequest(t, "GET", link+"/issues"), http.StatusOK).Body)
			assert.Equal(t, "WIP: an issue", strings.TrimSpace(doc.Find("#issue-list a.issue-title").First().Text()))
			assert.Equal(t, 0, doc.Find(`#issue-list [data-test-name="damast-draft-badge"]`).Length())
		})

		t.Run("ready for review requests code owners exactly then", func(t *testing.T) {
			setTitle(t, owner, pr, "My feature", http.StatusOK) // what the Ready for review button sends
			pr = getPR(t, "draft-1")
			assert.False(t, pr.IsWorkInProgress(t.Context()))
			assert.Equal(t, 1, damastCodeOwnerRequests(t, pr))
		})

		t.Run("convert to draft", func(t *testing.T) {
			doc := NewHTMLParser(t, owner.MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("%s/pulls/%d", link, pr.Index)), http.StatusOK).Body)
			assert.Equal(t, 1, doc.Find(`[data-test-name="damast-convert-to-draft"]`).Length())
			setTitle(t, owner, pr, "WIP: My feature", http.StatusOK)
			pr = getPR(t, "draft-1")
			assert.True(t, pr.IsWorkInProgress(t.Context()))
			// ready again does not duplicate the request
			setTitle(t, owner, pr, "My feature", http.StatusOK)
			assert.Equal(t, 1, damastCodeOwnerRequests(t, getPR(t, "draft-1")))
		})

		t.Run("convert to draft cancels a scheduled auto merge", func(t *testing.T) {
			pr = getPR(t, "draft-1")
			require.False(t, pr.IsWorkInProgress(t.Context()))
			scheduled, err := automerge.ScheduleAutoMerge(t.Context(), user2, pr, repo_model.MergeStyleMerge, "", false)
			require.NoError(t, err)
			require.True(t, scheduled)

			setTitle(t, owner, pr, "WIP: My feature", http.StatusOK)
			exist, _, err := pull_model.GetScheduledMergeByPullID(t.Context(), pr.ID)
			require.NoError(t, err)
			assert.False(t, exist, "a draft has no auto merge, like on GitHub")
			unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{IssueID: pr.IssueID, Type: issues_model.CommentTypePRUnScheduledToAutoMerge, PosterID: user2.ID})

			setTitle(t, owner, pr, "My feature", http.StatusOK)
			pr = getPR(t, "draft-1")
			assert.False(t, pr.HasMerged, "ready for review does not merge")
		})

		t.Run("manual review request while draft", func(t *testing.T) {
			pr3 := createDraft(t, "draft-3", "Manual request")
			owner.MakeRequest(t, NewRequestWithValues(t, "POST", link+"/issues/request_review", map[string]string{
				"issue_ids": fmt.Sprint(pr3.IssueID),
				"id":        "5",
				"action":    "attach",
			}), http.StatusOK)
			assert.Equal(t, 1, damastCodeOwnerRequests(t, pr3), "explicit review requests are allowed on drafts, like on GitHub")

			// the requested reviewer sees the draft in notifications with the badge
			doc := NewHTMLParser(t, loginUser(t, "user5").MakeRequest(t, NewRequest(t, "GET", "/notifications"), http.StatusOK).Body)
			var found bool
			doc.Find(".notifications-item").Each(func(_ int, item *goquery.Selection) {
				if strings.Contains(item.Find(".issue-title").Text(), "Manual request") {
					found = true
					assert.NotContains(t, item.Find(".issue-title").Text(), "WIP")
					assert.Equal(t, 1, item.Find(`[data-test-name="damast-draft-badge"]`).Length())
				}
			})
			assert.True(t, found, "notification for the draft PR")
		})

		t.Run("API", func(t *testing.T) {
			damastDraftBranch(t, repo, user2, "api-draft")
			req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/pulls", repo.FullName()), &api.CreatePullRequestOption{
				Head: "api-draft", Base: "main", Title: "From the API", Draft: true,
			}).AddTokenAuth(ownerToken)
			var apiPR api.PullRequest
			DecodeJSON(t, MakeRequest(t, req, http.StatusCreated), &apiPR)
			assert.True(t, apiPR.Draft)
			assert.Equal(t, "WIP: From the API", apiPR.Title)
			assert.Zero(t, damastCodeOwnerRequests(t, getPR(t, "api-draft")))

			// ready through an API title edit creates the code owner requests
			req = NewRequestWithJSON(t, "PATCH", fmt.Sprintf("/api/v1/repos/%s/pulls/%d", repo.FullName(), apiPR.Index), &api.EditPullRequestOption{
				Title: "From the API",
			}).AddTokenAuth(ownerToken)
			DecodeJSON(t, MakeRequest(t, req, http.StatusCreated), &apiPR)
			assert.False(t, apiPR.Draft)
			assert.Equal(t, 1, damastCodeOwnerRequests(t, getPR(t, "api-draft")))

			// convert to draft and back through the issue API (what mast uses)
			editIssueTitle := func(title string) {
				req := NewRequestWithJSON(t, "PATCH", fmt.Sprintf("/api/v1/repos/%s/issues/%d", repo.FullName(), apiPR.Index), &api.EditIssueOption{
					Title: title,
				}).AddTokenAuth(ownerToken)
				var issue api.Issue
				DecodeJSON(t, MakeRequest(t, req, http.StatusCreated), &issue)
				assert.Equal(t, title, issue.Title)
			}
			editIssueTitle("WIP: From the API")
			assert.True(t, getPR(t, "api-draft").IsWorkInProgress(t.Context()))
			editIssueTitle("From the API")
			assert.False(t, getPR(t, "api-draft").IsWorkInProgress(t.Context()))
			assert.Equal(t, 1, damastCodeOwnerRequests(t, getPR(t, "api-draft")))

			// a WIP title without the flag is a draft too (same storage)
			damastDraftBranch(t, repo, user2, "api-wip")
			req = NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/pulls", repo.FullName()), &api.CreatePullRequestOption{
				Head: "api-wip", Base: "main", Title: "WIP: Prefixed",
			}).AddTokenAuth(ownerToken)
			DecodeJSON(t, MakeRequest(t, req, http.StatusCreated), &apiPR)
			assert.True(t, apiPR.Draft)
			assert.Equal(t, "WIP: Prefixed", apiPR.Title)
		})

		t.Run("browser title and timeline cross-references hide the prefix", func(t *testing.T) {
			draftPR := createDraft(t, "draft-xref", "Cross ref")
			issueReq := NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/issues", repo.FullName()), &api.CreateIssueOption{Title: "Referenced issue"}).AddTokenAuth(ownerToken)
			var issue api.Issue
			DecodeJSON(t, MakeRequest(t, issueReq, http.StatusCreated), &issue)
			body := fmt.Sprintf("refs #%d", issue.Index)
			MakeRequest(t, NewRequestWithJSON(t, "PATCH", fmt.Sprintf("/api/v1/repos/%s/pulls/%d", repo.FullName(), draftPR.Index), &api.EditPullRequestOption{Body: &body}).AddTokenAuth(ownerToken), http.StatusCreated)

			doc := NewHTMLParser(t, owner.MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("%s/pulls/%d", link, draftPR.Index)), http.StatusOK).Body)
			assert.True(t, strings.HasPrefix(doc.Find("head title").Text(), fmt.Sprintf("#%d - Cross ref - ", draftPR.Index)), doc.Find("head title").Text())

			doc = NewHTMLParser(t, owner.MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("%s/issues/%d", link, issue.Index)), http.StatusOK).Body)
			assert.Equal(t, "Cross ref", strings.TrimSpace(doc.Find(".timeline-item.event .detail b").First().Text()))
		})

		t.Run("drafts disabled without prefixes", func(t *testing.T) {
			defer test.MockVariableValue(&setting.Repository.PullRequest.WorkInProgressPrefixes, []string{})()
			// (upstream's compare page needs at least one prefix itself, so only the API and the sidebar are checked)
			damastDraftBranch(t, repo, user2, "no-prefixes")

			req := NewRequestWithJSON(t, "POST", fmt.Sprintf("/api/v1/repos/%s/pulls", repo.FullName()), &api.CreatePullRequestOption{
				Head: "no-prefixes", Base: "main", Title: "No prefixes", Draft: true,
			}).AddTokenAuth(ownerToken)
			MakeRequest(t, req, http.StatusUnprocessableEntity)

			doc := NewHTMLParser(t, owner.MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("%s/pulls/%d", link, pr.Index)), http.StatusOK).Body)
			assert.Equal(t, 0, doc.Find(`.toggle-wip`).Length())
		})

		t.Run("no toggles in an archived repository", func(t *testing.T) {
			draftPR := createDraft(t, "draft-archived", "Archived draft")
			archived := true
			MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/repos/"+repo.FullName(), &api.EditRepoOption{Archived: &archived}).AddTokenAuth(ownerToken), http.StatusOK)
			doc := NewHTMLParser(t, owner.MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("%s/pulls/%d", link, draftPR.Index)), http.StatusOK).Body)
			assert.Equal(t, 0, doc.Find(`[data-test-name="damast-ready-for-review"]`).Length())
			assert.Equal(t, 0, doc.Find(`.toggle-wip a`).Length())
		})
	})
}
