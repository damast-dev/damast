// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
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

		// commit view: names are shown
		doc = NewHTMLParser(t, session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/commit/"+sha), http.StatusOK).Body)
		authors := doc.doc.Find(".commit-header-row .author .damast-coauthor")
		assert.Equal(t, 2, authors.Length())
		assert.Contains(t, authors.Text(), "Stranger")

		// commits without trailer render no co-authors
		doc = NewHTMLParser(t, session.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/commit/65f1bf27bc3bf70f64657658635e66094edbcb4d"), http.StatusOK).Body)
		assert.Equal(t, 0, doc.doc.Find(".damast-coauthor").Length())
	})
}
