// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"bytes"
	"net/http"
	"net/url"
	"strings"
	"testing"

	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	"forgejo.org/modules/test"
	files_service "forgejo.org/services/repository/files"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDamastCoAuthors(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, _ *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		repo1 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
		resp, err := files_service.ChangeRepoFiles(git.DefaultContext, repo1, user2, &files_service.ChangeRepoFilesOptions{
			Files:     []*files_service.ChangeRepoFile{{Operation: "create", TreePath: "damast.txt", ContentReader: strings.NewReader("x")}},
			OldBranch: "master",
			Message:   "pair work\n\nCo-authored-by: User One <user1@example.com>\nCo-authored-by: Stranger <stranger@example.org>\n",
		})
		require.NoError(t, err)
		sha := resp.Commit.SHA

		session := loginUser(t, "user2")

		// commit list: first row shows both co-authors, known user links to the profile
		doc := NewHTMLParser(t, session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/commits/branch/master"), http.StatusOK).Body)
		row := doc.doc.Find("#commits-table tbody tr").First()
		assert.Equal(t, 2, row.Find(".damast-coauthor").Length())
		href, _ := row.Find("a.damast-coauthor").Attr("href")
		assert.Equal(t, "/user1", href)
		// GitHub-style stack: author avatar first, then the co-authors; names collapse to "N others"
		assert.Equal(t, 1, row.Find(".damast-avatar-stack .damast-author").Length())
		assert.Contains(t, row.Find(".damast-author-names").Text(), "2 others")

		// commit view: names are shown
		doc = NewHTMLParser(t, session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/commit/"+sha), http.StatusOK).Body)
		authors := doc.doc.Find(".commit-header-row .author .damast-coauthor")
		assert.Equal(t, 2, authors.Length())
		names := doc.doc.Find(".commit-header-row .author .damast-author-names").Text()
		assert.Contains(t, names, "User One")
		assert.Contains(t, names, "Stranger")
		assert.Contains(t, names, " and ")

		// more than four co-authors: four avatars plus a "+N" counter
		var many strings.Builder
		many.WriteString("mob session\n\n")
		for _, n := range []string{"a", "b", "c", "d", "e", "f"} {
			many.WriteString("Co-authored-by: Mob " + n + " <mob-" + n + "@example.org>\n")
		}
		resp, err = files_service.ChangeRepoFiles(git.DefaultContext, repo1, user2, &files_service.ChangeRepoFilesOptions{
			Files:     []*files_service.ChangeRepoFile{{Operation: "create", TreePath: "damast-mob.txt", ContentReader: strings.NewReader("x")}},
			OldBranch: "master",
			Message:   many.String(),
		})
		require.NoError(t, err)
		doc = NewHTMLParser(t, session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/commit/"+resp.Commit.SHA), http.StatusOK).Body)
		assert.Equal(t, 4, doc.doc.Find(".commit-header-row .damast-coauthor").Length())
		assert.Equal(t, "+2", strings.TrimSpace(doc.doc.Find(".commit-header-row .damast-coauthor-more").Text()))

		// "+N" tooltip separates the hidden names
		tip, _ := doc.doc.Find(".commit-header-row .damast-coauthor-more").Attr("data-tooltip-content")
		assert.Equal(t, "Mob e, Mob f", tip)

		// hostile and odd trailers, on a PR branch so the PR commits tab is covered too
		resp, err = files_service.ChangeRepoFiles(git.DefaultContext, repo1, user2, &files_service.ChangeRepoFilesOptions{
			Files:     []*files_service.ChangeRepoFile{{Operation: "create", TreePath: "damast-edge.txt", ContentReader: strings.NewReader("x")}},
			OldBranch: "master",
			NewBranch: "damast-coauthor-edge",
			Message: "edge\n\n" +
				"Co-authored-by: <img src=x onerror=alert(1)> \"q <xss@example.org>\n" +
				"Co-authored-by: User One <user1@example.com>\n" +
				"Co-authored-by: User One Again <user1-2@example.com>\n" + // same account, other email: shown once
				"Co-authored-by: dependabot[bot] <49699333+dependabot[bot]@users.noreply.github.com>\n" +
				"Co-authored-by: Not Verified <user2-2@example.com>\n", // unverified email of user2: no link
		})
		require.NoError(t, err)
		for _, link := range []string{"/user2/repo1/commit/" + resp.Commit.SHA, ""} {
			if link == "" {
				pr := testPullCreate(t, session, "user2", "repo1", true, "master", "damast-coauthor-edge", "co-author edge")
				link = test.RedirectURL(pr) + "/commits"
			}
			body := session.MakeRequest(t, NewRequest(t, "GET", link), http.StatusOK).Body.String()
			assert.NotContains(t, body, "<img src=x onerror", link)
			doc = NewHTMLParser(t, bytes.NewBufferString(body))
			stack := doc.doc.Find(".damast-avatar-stack").First()
			assert.Equal(t, 4, stack.Find(".damast-coauthor").Length(), link)
			assert.Equal(t, 1, stack.Find("a.damast-coauthor").Length(), link)
			assert.Equal(t, 1, stack.Find(`a.damast-coauthor[href="/user1"]`).Length(), link)
			tip, _ := stack.Find("span.damast-coauthor").First().Attr("data-tooltip-content")
			assert.Equal(t, `<img src=x onerror=alert(1)> "q <xss@example.org>`, tip, link)
		}

		// commits without trailer render no co-authors
		doc = NewHTMLParser(t, session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/commit/65f1bf27bc3bf70f64657658635e66094edbcb4d"), http.StatusOK).Body)
		assert.Equal(t, 0, doc.doc.Find(".damast-coauthor").Length())
	})
}
