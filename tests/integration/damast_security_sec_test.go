// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"testing"

	"forgejo.org/models/damast"
	"forgejo.org/models/db"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/structs"
	"forgejo.org/modules/test"
	"forgejo.org/services/damast/security"
	"forgejo.org/services/mailer"
	repo_service "forgejo.org/services/repository"
	user_service "forgejo.org/services/user"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Blocking a user ends their access as reporter or advisory collaborator, as it ends a repository collaboration:
// 404 on the advisory, no comments, no mail. The admins still see who reported it.
func TestDamastSecurityBlockedParticipant(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := db.DefaultContext
	var box secMailbox
	defer box.mock()()
	owner := forgery.CreateUser(t, nil)
	reporter := forgery.CreateUser(t, nil)
	collaborator := forgery.CreateUser(t, nil)
	repo := forgery.CreateRepository(t, owner, nil)
	require.NoError(t, damast.SetPrivateReporting(ctx, repo, true))
	a, err := security.Report(ctx, repo, reporter, &security.Input{Title: "blocked", Description: "BLOCKSECRET", Severity: "low"})
	require.NoError(t, err)
	require.NoError(t, security.AddCollaborator(ctx, repo, owner, a, collaborator))
	link := fmt.Sprintf("/%s/security/advisories/%s", repo.FullName(), a.AdvisoryID)
	api := fmt.Sprintf("/api/v1/repos/%s/security-advisories/%s", repo.FullName(), a.AdvisoryID)
	for _, u := range []*user_model.User{reporter, collaborator} {
		loginUser(t, u.Name).MakeRequest(t, NewRequest(t, "GET", link), http.StatusOK)
		require.NoError(t, user_service.BlockUser(ctx, owner.ID, u.ID))
	}
	box.take()

	for _, u := range []*user_model.User{reporter, collaborator} {
		session := loginUser(t, u.Name)
		session.MakeRequest(t, NewRequest(t, "GET", link), http.StatusNotFound)
		session.MakeRequest(t, NewRequestWithValues(t, "POST", link+"/comments", map[string]string{"content": "still here"}), http.StatusNotFound)
		token := forgery.CreateAccessToken(t, u, nil).Token
		MakeRequest(t, NewRequest(t, "GET", api).AddTokenAuth(token), http.StatusNotFound)
		list := MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/"+repo.FullName()+"/security-advisories").AddTokenAuth(token), http.StatusOK).Body.String()
		assert.NotContains(t, list, a.AdvisoryID)
	}
	_, err = security.AddComment(ctx, repo, owner, a, "update from the maintainer")
	require.NoError(t, err)
	assert.Empty(t, box.take(reporter, collaborator))

	page := loginUser(t, owner.Name).MakeRequest(t, NewRequest(t, "GET", link), http.StatusOK).Body.String()
	assert.Contains(t, page, reporter.Name)
}

// Only users with a public profile can be credited, and a credited user whose profile later stops being public
// disappears from every public view of the advisory.
func TestDamastSecurityCreditsPublicOnly(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&mailer.SendAsync, func(...*mailer.Message) {})()
	ctx := db.DefaultContext
	owner := forgery.CreateUser(t, nil)
	hidden := forgery.CreateUser(t, nil)
	later := forgery.CreateUser(t, nil)
	setVisibility := func(u *user_model.User, v structs.VisibleType) {
		_, err := db.GetEngine(ctx).ID(u.ID).Cols("visibility").Update(&user_model.User{Visibility: v})
		require.NoError(t, err)
	}
	setVisibility(hidden, structs.VisibleTypePrivate)
	repo := forgery.CreateRepository(t, owner, nil)
	token := forgery.CreateAccessToken(t, owner, nil).Token
	base := "/api/v1/repos/" + repo.FullName() + "/security-advisories"
	body := func(login string) map[string]any {
		return map[string]any{"summary": "credits", "description": "d", "severity": "low", "credits": []map[string]string{{"login": login}}}
	}

	resp := MakeRequest(t, NewRequestWithJSON(t, "POST", base, body(hidden.Name)).AddTokenAuth(token), http.StatusUnprocessableEntity)
	assert.Contains(t, resp.Body.String(), hidden.Name)
	page := loginUser(t, owner.Name).MakeRequest(t, NewRequestWithURLValues(t, "POST", "/"+repo.FullName()+"/security/advisories/new", url.Values{
		"title": {"credits"}, "description": {"d"}, "severity": {"low"}, "credits": {hidden.Name},
	}), http.StatusOK).Body.String()
	assert.Contains(t, page, "cannot be credited")
	assert.Equal(t, 0, unittest.GetCount(t, &damast.SecurityAdvisory{RepoID: repo.ID}))

	var created security.APIAdvisory
	DecodeJSON(t, MakeRequest(t, NewRequestWithJSON(t, "POST", base, body(later.Name)).AddTokenAuth(token), http.StatusCreated), &created)
	MakeRequest(t, NewRequestWithJSON(t, "PATCH", base+"/"+created.GhsaID, map[string]any{"state": "published"}).AddTokenAuth(token), http.StatusOK)
	setVisibility(later, structs.VisibleTypeLimited)
	for _, path := range []string{base + "/" + created.GhsaID, base + "/" + created.GhsaID + "/osv", base + "/osv"} {
		assert.NotContains(t, MakeRequest(t, NewRequest(t, "GET", path), http.StatusOK).Body.String(), later.Name, path)
	}
	assert.NotContains(t, MakeRequest(t, NewRequest(t, "GET", "/"+repo.FullName()+"/security/advisories/"+created.GhsaID), http.StatusOK).Body.String(), later.Name)
	// editing other fields still works and drops the hidden credit instead of failing on it
	MakeRequest(t, NewRequestWithJSON(t, "PATCH", base+"/"+created.GhsaID, map[string]any{"summary": "renamed"}).AddTokenAuth(token), http.StatusOK)
}

// Concurrent reports cannot get past the per-repository cap.
func TestDamastSecurityReportCapConcurrent(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&mailer.SendAsync, func(...*mailer.Message) {})()
	ctx := db.DefaultContext
	owner := forgery.CreateUser(t, nil)
	reporter := forgery.CreateUser(t, nil)
	repo := forgery.CreateRepository(t, owner, nil)
	require.NoError(t, damast.SetPrivateReporting(ctx, repo, true))

	var wg sync.WaitGroup
	var mu sync.Mutex
	var ok, capped int
	for i := range 3 * security.MaxOpenReportsPerRepo {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := security.Report(ctx, repo, reporter, &security.Input{Title: fmt.Sprint("race ", i), Description: "d", Severity: "low"})
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, security.ErrTooManyReports):
				capped++
			default: // e.g. SQLite's busy timeout under this artificial load; it must not create a report either
				t.Logf("report: %v", err)
			}
		}()
	}
	wg.Wait()
	assert.Equal(t, security.MaxOpenReportsPerRepo, ok)
	assert.Positive(t, capped)
	assert.Equal(t, security.MaxOpenReportsPerRepo, unittest.GetCount(t, &damast.SecurityAdvisory{RepoID: repo.ID}))
}

// Advisory and setting rows older than their repository belong to an earlier repository with the same id (deleted
// while the instance ran plain Forgejo). They are ignored, but reading never deletes anything.
func TestDamastSecurityOrphanAdvisory(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&mailer.SendAsync, func(...*mailer.Message) {})()
	ctx := db.DefaultContext
	owner := forgery.CreateUser(t, nil)
	repo := forgery.CreateRepository(t, owner, nil)
	orphan, err := security.Create(ctx, repo, owner, &security.Input{Title: "ORPHANTITLE", Description: "d", Severity: "low"})
	require.NoError(t, err)
	_, err = security.AddComment(ctx, repo, owner, orphan, "orphan comment")
	require.NoError(t, err)
	require.NoError(t, security.SetState(ctx, repo, owner, orphan, damast.AdvisoryPublished))
	_, err = db.GetEngine(ctx).Exec("UPDATE damast_security_advisory SET created_unix = ? WHERE id = ?", int64(repo.CreatedUnix)-3600, orphan.ID)
	require.NoError(t, err)
	fresh, err := security.Create(ctx, repo, owner, &security.Input{Title: "FRESHTITLE", Description: "d", Severity: "low"})
	require.NoError(t, err)

	session := loginUser(t, owner.Name)
	session.MakeRequest(t, NewRequest(t, "GET", "/"+repo.FullName()+"/security/advisories/"+orphan.AdvisoryID), http.StatusNotFound)
	overview := session.MakeRequest(t, NewRequest(t, "GET", "/"+repo.FullName()+"/security"), http.StatusOK).Body.String()
	assert.NotContains(t, overview, "ORPHANTITLE")
	assert.Contains(t, overview, "FRESHTITLE")
	base := "/api/v1/repos/" + repo.FullName() + "/security-advisories"
	MakeRequest(t, NewRequest(t, "GET", base+"/"+orphan.AdvisoryID), http.StatusNotFound)
	assert.NotContains(t, MakeRequest(t, NewRequest(t, "GET", base+"/osv"), http.StatusOK).Body.String(), "ORPHANTITLE")

	unittest.AssertExistsAndLoadBean(t, &damast.SecurityAdvisory{ID: orphan.ID})
	unittest.AssertExistsAndLoadBean(t, &damast.AdvisoryComment{AdvisoryID: orphan.ID})
	unittest.AssertExistsAndLoadBean(t, &damast.SecurityAdvisory{ID: fresh.ID})

	// an orphaned setting does not switch private reporting on; saving the settings replaces it
	reporter := forgery.CreateUser(t, nil)
	reporterSession := loginUser(t, reporter.Name)
	require.NoError(t, damast.SetPrivateReporting(ctx, repo, true))
	reporterSession.MakeRequest(t, NewRequest(t, "GET", "/"+repo.FullName()+"/security/report"), http.StatusOK)
	_, err = db.GetEngine(ctx).Exec("UPDATE damast_security_setting SET created_unix = ? WHERE repo_id = ?", int64(repo.CreatedUnix)-3600, repo.ID)
	require.NoError(t, err)
	reporterSession.MakeRequest(t, NewRequest(t, "GET", "/"+repo.FullName()+"/security/report"), http.StatusNotFound)
	has, err := db.GetEngine(ctx).ID(repo.ID).Exist(new(damast.SecuritySetting))
	require.NoError(t, err)
	assert.True(t, has, "an orphaned setting is ignored, not deleted")
	session.MakeRequest(t, NewRequestWithValues(t, "POST", "/"+repo.FullName()+"/security/settings", map[string]string{"private_reporting": "on"}), http.StatusSeeOther)
	reporterSession.MakeRequest(t, NewRequest(t, "GET", "/"+repo.FullName()+"/security/report"), http.StatusOK)
}

// The orphan rule never hides the data of the repository itself: a repository is always older than its advisories
// and settings (created_unix is only set on insert), also after a transfer, and a row from the very second the
// repository was created counts as its own.
func TestDamastSecurityOldRepositoryKeepsAdvisories(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&mailer.SendAsync, func(...*mailer.Message) {})()
	ctx := db.DefaultContext
	owner := forgery.CreateUser(t, nil)
	newOwner := forgery.CreateUser(t, nil)
	reporter := forgery.CreateUser(t, nil)
	repo := forgery.CreateRepository(t, owner, &forgery.CreateRepositoryOptions{SkipCleanup: true})
	// a repository created years ago, like a long-lived or migrated one
	_, err := db.GetEngine(ctx).Exec("UPDATE repository SET created_unix = ? WHERE id = ?", int64(repo.CreatedUnix)-5*365*24*3600, repo.ID)
	require.NoError(t, err)
	repo = unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
	require.NoError(t, damast.SetPrivateReporting(ctx, repo, true))
	a, err := security.Report(ctx, repo, reporter, &security.Input{Title: "OLDREPO", Description: "d", Severity: "low"})
	require.NoError(t, err)
	same, err := security.Create(ctx, repo, owner, &security.Input{Title: "SAMESECOND", Description: "d", Severity: "low"})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).Exec("UPDATE damast_security_advisory SET created_unix = ? WHERE id = ?", int64(repo.CreatedUnix), same.ID)
	require.NoError(t, err)

	require.NoError(t, repo_service.TransferOwnership(ctx, owner, newOwner, repo, nil))
	repo = unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
	session := loginUser(t, newOwner.Name)
	page := session.MakeRequest(t, NewRequest(t, "GET", "/"+repo.FullName()+"/security"), http.StatusOK).Body.String()
	assert.Contains(t, page, "OLDREPO")
	assert.Contains(t, page, "SAMESECOND")
	session.MakeRequest(t, NewRequest(t, "GET", "/"+repo.FullName()+"/security/advisories/"+a.AdvisoryID), http.StatusOK)
	loginUser(t, reporter.Name).MakeRequest(t, NewRequest(t, "GET", "/"+repo.FullName()+"/security/report"), http.StatusOK)
}

// A public-only token is limited to public data. Drafts, reports and private discussions in a public repository
// are not public, so such a token gets exactly what an anonymous visitor gets, even for the repository's owner.
func TestDamastSecurityPublicOnlyToken(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&mailer.SendAsync, func(...*mailer.Message) {})()
	ctx := db.DefaultContext
	owner := forgery.CreateUser(t, nil)
	repo := forgery.CreateRepository(t, owner, &forgery.CreateRepositoryOptions{Files: forgery.FilesInit{}})
	require.NoError(t, damast.SetPrivateReporting(ctx, repo, true))
	in := func() *security.Input {
		return &security.Input{Title: "PUBONLY title", Description: "PUBONLY secret", Severity: "high"}
	}
	draft, err := security.Create(ctx, repo, owner, in())
	require.NoError(t, err)
	_, err = security.AddComment(ctx, repo, owner, draft, "PUBONLY comment")
	require.NoError(t, err)
	published, err := security.Create(ctx, repo, owner, in())
	require.NoError(t, err)
	require.NoError(t, security.SetState(ctx, repo, owner, published, damast.AdvisoryPublished))

	token := forgery.CreateAccessToken(t, owner, &forgery.CreateAccessTokenOptions{Scope: "public-only,write:repository"}).Token
	base := "/api/v1/repos/" + repo.FullName() + "/security-advisories"
	do := func(method, path string, body any, status int) string {
		t.Helper()
		var req *RequestWrapper
		if body == nil {
			req = NewRequest(t, method, path)
		} else {
			req = NewRequestWithJSON(t, method, path, body)
		}
		return MakeRequest(t, req.AddTokenAuth(token), status).Body.String()
	}

	list := do("GET", base, nil, http.StatusOK)
	assert.Contains(t, list, published.AdvisoryID)
	assert.NotContains(t, list, draft.AdvisoryID)
	do("GET", base+"/"+draft.AdvisoryID, nil, http.StatusNotFound)
	do("GET", base+"/"+draft.AdvisoryID+"/comments", nil, http.StatusNotFound)
	do("GET", base+"/"+published.AdvisoryID, nil, http.StatusOK)
	do("GET", base+"/"+published.AdvisoryID+"/comments", nil, http.StatusNotFound)
	do("PATCH", base+"/"+published.AdvisoryID, map[string]any{"summary": "pwned"}, http.StatusNotFound)
	do("POST", base, map[string]any{"summary": "x", "description": "y", "severity": "low"}, http.StatusNotFound)
	do("POST", base+"/reports", map[string]any{"summary": "x", "description": "y", "severity": "low"}, http.StatusNotFound)

	// the same user with a normal token still manages everything
	full := forgery.CreateAccessToken(t, owner, nil).Token
	MakeRequest(t, NewRequest(t, "GET", base+"/"+draft.AdvisoryID).AddTokenAuth(full), http.StatusOK)
}

// Two admins act on the same advisory at once: one publishes it, the other (whose page still shows the draft)
// closes it. The second change must fail instead of silently un-publishing an advisory, which only "withdraw" may.
func TestDamastSecurityStaleStateChange(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&mailer.SendAsync, func(...*mailer.Message) {})()
	ctx := db.DefaultContext
	owner := forgery.CreateUser(t, nil)
	repo := forgery.CreateRepository(t, owner, nil)
	a, err := security.Create(ctx, repo, owner, &security.Input{Title: "stale", Description: "d", Severity: "low"})
	require.NoError(t, err)
	stale, err := damast.GetAdvisory(ctx, repo.ID, a.AdvisoryID)
	require.NoError(t, err)

	require.NoError(t, security.SetState(ctx, repo, owner, a, damast.AdvisoryPublished))
	require.Error(t, security.SetState(ctx, repo, owner, stale, damast.AdvisoryClosed))
	got := unittest.AssertExistsAndLoadBean(t, &damast.SecurityAdvisory{ID: a.ID})
	assert.Equal(t, damast.AdvisoryPublished, got.State)
	assert.Zero(t, got.ClosedUnix)
}
