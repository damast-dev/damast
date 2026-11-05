// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	activities_model "forgejo.org/models/activities"
	"forgejo.org/models/damast"
	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	"forgejo.org/models/perm"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	webhook_model "forgejo.org/models/webhook"
	repo_module "forgejo.org/modules/repository"
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

type secMailbox struct {
	sync.Mutex
	msgs []*mailer.Message
}

func (m *secMailbox) mock() func() {
	return test.MockVariableValue(&mailer.SendAsync, func(msgs ...*mailer.Message) {
		m.Lock()
		defer m.Unlock()
		m.msgs = append(m.msgs, msgs...)
	})
}

// take returns the recipients' user names since the last call, sorted.
func (m *secMailbox) take(users ...*user_model.User) []string {
	m.Lock()
	defer m.Unlock()
	var names []string
	for _, msg := range m.msgs {
		name := "unknown:" + msg.To
		for _, u := range users {
			if strings.Contains(msg.To, u.Email) {
				name = u.Name
			}
		}
		names = append(names, name)
	}
	m.msgs = nil
	slices.Sort(names)
	return names
}

func secNames(users ...*user_model.User) []string {
	var names []string
	for _, u := range users {
		names = append(names, u.Name)
	}
	slices.Sort(names)
	return names
}

// TestDamastSecurityFlow: report, triage, collaborate, publish, edit. Checks who gets mail at every step and
// that nothing reaches Forgejo's own tables (actions, notifications, issues, comments, webhook deliveries).
func TestDamastSecurityFlow(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := db.DefaultContext
	var box secMailbox
	defer box.mock()()

	owner := forgery.CreateUser(t, nil)
	reporter := forgery.CreateUser(t, nil)
	collaborator := forgery.CreateUser(t, nil)
	watcher := forgery.CreateUser(t, nil)
	mentioned := forgery.CreateUser(t, nil)
	stranger := forgery.CreateUser(t, nil)
	everyone := []*user_model.User{owner, reporter, collaborator, watcher, mentioned, stranger}
	repo := forgery.CreateRepository(t, owner, &forgery.CreateRepositoryOptions{Files: forgery.FilesInit{}})
	require.NoError(t, repo_model.WatchRepoExplicitly(ctx, watcher.ID, repo.ID, repo_model.WatchAllSelection))
	require.NoError(t, repo_model.WatchRepoExplicitly(ctx, mentioned.ID, repo.ID, repo_model.WatchAllSelection))
	ownerToken := forgery.CreateAccessToken(t, owner, nil).Token
	MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/repos/"+repo.FullName()+"/hooks", map[string]any{
		"type": "forgejo", "active": true, "events": []string{"issues", "issue_comment", "repository", "release", "push"},
		"config": map[string]string{"url": "http://127.0.0.1:9/hook", "content_type": "json"},
	}).AddTokenAuth(ownerToken), http.StatusCreated)

	counts := func() map[string]int {
		return map[string]int{
			"action":       unittest.GetCount(t, &activities_model.Action{}),
			"notification": unittest.GetCount(t, &activities_model.Notification{}),
			"issue":        unittest.GetCount(t, &issues_model.Issue{}),
			"comment":      unittest.GetCount(t, &issues_model.Comment{}),
			"hook_task":    unittest.GetCount(t, &webhook_model.HookTask{}),
		}
	}
	before := counts()
	box.take()

	ownerSession := loginUser(t, owner.Name)
	reporterSession := loginUser(t, reporter.Name)
	base := "/" + repo.FullName() + "/security"

	// reporting is off by default
	reporterSession.MakeRequest(t, NewRequest(t, "GET", base+"/report"), http.StatusNotFound)
	ownerSession.MakeRequest(t, NewRequestWithValues(t, "POST", base+"/settings", map[string]string{"private_reporting": "on"}), http.StatusSeeOther)

	marker := "LEAKMARK" + strings.ToLower(damast.NewAdvisoryID())
	resp := reporterSession.MakeRequest(t, NewRequestWithURLValues(t, "POST", base+"/report", url.Values{
		"title":       {marker + " title"},
		"description": {marker + " body, see #1 and ping @" + mentioned.Name + " and @" + watcher.Name},
		"severity":    {"high"},
		"cvss_vector": {"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"},
		"ecosystem":   {"npm", "npm"}, "package": {"tool", ""}, "vulnerable": {">= 1.0, < 1.4.2", ""}, "patched": {"1.4.2", ""},
	}), http.StatusSeeOther)
	link := resp.Header().Get("Location")
	require.Contains(t, link, base+"/advisories/DAMAST-")
	id := link[strings.LastIndex(link, "/")+1:]
	a, err := damast.GetAdvisory(ctx, repo.ID, id)
	require.NoError(t, err)
	assert.Equal(t, damast.AdvisoryTriage, a.State)
	assert.Equal(t, "critical", a.Severity, "severity follows the CVSS score")
	assert.Equal(t, reporter.ID, a.ReporterID)
	assert.Len(t, a.Affected, 1, "the empty product row is ignored")
	assert.Equal(t, secNames(owner), box.take(everyone...), "a new report mails the admins only")

	reporterSession.MakeRequest(t, NewRequest(t, "GET", link), http.StatusOK)
	MakeRequest(t, NewRequest(t, "GET", link), http.StatusNotFound)

	// accept, add a collaborator, discuss
	ownerSession.MakeRequest(t, NewRequestWithValues(t, "POST", link+"/state", map[string]string{"state": "draft"}), http.StatusSeeOther)
	assert.Equal(t, secNames(reporter), box.take(everyone...))
	ownerSession.MakeRequest(t, NewRequestWithValues(t, "POST", link+"/collaborators", map[string]string{"username": collaborator.Name}), http.StatusSeeOther)
	assert.Equal(t, secNames(collaborator), box.take(everyone...))
	loginUser(t, collaborator.Name).MakeRequest(t, NewRequest(t, "GET", link), http.StatusOK)
	ownerSession.MakeRequest(t, NewRequestWithValues(t, "POST", link+"/comments", map[string]string{"content": marker + " comment for @" + mentioned.Name}), http.StatusSeeOther)
	assert.Equal(t, secNames(reporter, collaborator), box.take(everyone...), "mentions never notify")
	reporterSession.MakeRequest(t, NewRequestWithValues(t, "POST", link+"/comments", map[string]string{"content": marker + " reply"}), http.StatusSeeOther)
	assert.Equal(t, secNames(owner, collaborator), box.take(everyone...), "a reporter's reply reaches the admins")

	// nothing of the draft is visible anywhere else
	secAssertNoMarker(t, marker, id, repo, reporter, watcher, mentioned, stranger)
	assert.Equal(t, before, counts(), "advisories must not create Forgejo rows")

	// publish: public, OSV, still private discussion
	ownerSession.MakeRequest(t, NewRequestWithValues(t, "POST", link+"/state", map[string]string{"state": "published"}), http.StatusSeeOther)
	assert.Equal(t, secNames(reporter, collaborator), box.take(everyone...))
	page := MakeRequest(t, NewRequest(t, "GET", link), http.StatusOK).Body.String()
	assert.Contains(t, page, marker+" title")
	assert.NotContains(t, page, marker+" comment", "comments stay private after publishing")
	assert.NotContains(t, page, marker+" reply")
	MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/"+repo.FullName()+"/security-advisories/"+id+"/comments"), http.StatusNotFound)

	var osv security.OSV
	DecodeJSON(t, MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/"+repo.FullName()+"/security-advisories/"+id+"/osv"), http.StatusOK), &osv)
	assert.Equal(t, id, osv.ID)
	assert.Equal(t, "1.6.0", osv.SchemaVersion)
	assert.NotEmpty(t, osv.Published)
	assert.Equal(t, []security.OSVEvent{{Introduced: "1.0"}, {Fixed: "1.4.2"}}, osv.Affected[0].Ranges[0].Events)
	var osvList []security.OSV
	DecodeJSON(t, MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/"+repo.FullName()+"/security-advisories/osv"), http.StatusOK), &osvList)
	require.Len(t, osvList, 1)

	// editing a published advisory moves "modified" past "published"
	_, err = db.GetEngine(ctx).Exec("UPDATE damast_security_advisory SET published_unix = published_unix - 3600, updated_unix = updated_unix - 3600 WHERE id = ?", a.ID)
	require.NoError(t, err)
	ownerSession.MakeRequest(t, NewRequestWithURLValues(t, "POST", link+"/edit", url.Values{
		"title": {marker + " title v2"}, "description": {"fixed in 1.4.2"}, "severity": {"high"},
		"ecosystem": {"npm"}, "package": {"tool"}, "vulnerable": {">= 1.0, < 1.4.2"}, "patched": {"1.4.2"},
	}), http.StatusSeeOther)
	DecodeJSON(t, MakeRequest(t, NewRequest(t, "GET", "/api/v1/repos/"+repo.FullName()+"/security-advisories/"+id+"/osv"), http.StatusOK), &osv)
	published, _ := time.Parse(time.RFC3339, osv.Published)
	modified, _ := time.Parse(time.RFC3339, osv.Modified)
	assert.Greater(t, modified.Sub(published), 30*time.Minute)
	assert.Equal(t, marker+" title v2", osv.Summary)

	assert.Equal(t, before, counts(), "publishing must not create Forgejo rows either")
	assert.Empty(t, box.take(everyone...), "editing does not mail")
}

// secAssertNoMarker fetches the places where Forgejo shows repository activity and checks the draft is absent.
func secAssertNoMarker(t *testing.T, marker, id string, repo *repo_model.Repository, reporter *user_model.User, viewers ...*user_model.User) {
	t.Helper()
	paths := []string{
		"/", "/explore/repos", "/" + repo.FullName(), "/" + repo.FullName() + "/issues", "/" + repo.FullName() + "/pulls",
		"/" + repo.FullName() + "/activity", "/" + repo.FullName() + ".rss", "/" + repo.FullName() + ".atom",
		"/" + repo.FullName() + "/issues?q=title", "/" + repo.FullName() + "/security", // the web search echoes q, so search a title word
		"/" + reporter.Name, "/" + reporter.Name + ".rss", "/" + reporter.Name + "?tab=activity",
		"/notifications", "/issues", "/" + repo.FullName() + "/issues/1",
		"/api/v1/repos/issues/search?q=" + marker, "/api/v1/repos/" + repo.FullName() + "/issues?q=" + marker,
		"/api/v1/users/" + reporter.Name + "/activities/feeds", "/api/v1/repos/" + repo.FullName() + "/activities/feeds",
		"/api/v1/notifications", "/api/v1/repos/" + repo.FullName() + "/security-advisories",
	}
	sessions := []*TestSession{emptyTestSession(t)}
	tokens := []string{""}
	for _, u := range viewers {
		sessions = append(sessions, loginUser(t, u.Name))
		tokens = append(tokens, forgery.CreateAccessToken(t, u, nil).Token)
	}
	for i, s := range sessions {
		for _, p := range paths {
			req := NewRequest(t, "GET", p)
			if strings.HasPrefix(p, "/api/") {
				req.AddTokenAuth(tokens[i])
			}
			body := s.MakeRequest(t, req, NoExpectedStatus).Body.String()
			assert.NotContains(t, body, marker, "viewer %d %s", i, p)
			assert.NotContains(t, body, id, "viewer %d %s", i, p)
		}
	}
}

func TestDamastSecurityReportCaps(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&mailer.SendAsync, func(...*mailer.Message) {})()
	ctx := db.DefaultContext
	owner := forgery.CreateUser(t, nil)
	reporter := forgery.CreateUser(t, nil)
	repo := forgery.CreateRepository(t, owner, nil)
	require.NoError(t, damast.SetPrivateReporting(ctx, repo.ID, true))
	token := forgery.CreateAccessToken(t, reporter, nil).Token
	report := func(status int) *security.APIAdvisory {
		resp := MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/repos/"+repo.FullName()+"/security-advisories/reports", map[string]any{
			"summary": "r", "description": "d", "severity": "low",
		}).AddTokenAuth(token), status)
		if status != http.StatusCreated {
			return nil
		}
		var r security.APIAdvisory
		DecodeJSON(t, resp, &r)
		return &r
	}
	var first *security.APIAdvisory
	for i := range security.MaxOpenReportsPerRepo {
		r := report(http.StatusCreated)
		if i == 0 {
			first = r
		}
	}
	report(http.StatusTooManyRequests)

	// closing a report frees its slot
	MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/repos/"+repo.FullName()+"/security-advisories/"+first.GhsaID, map[string]any{"state": "closed"}).
		AddTokenAuth(forgery.CreateAccessToken(t, owner, nil).Token), http.StatusOK)
	report(http.StatusCreated)

	// the daily cap counts reports in all repositories
	other := forgery.CreateRepository(t, owner, nil)
	require.NoError(t, damast.SetPrivateReporting(ctx, other.ID, true))
	for range security.MaxReportsPerDay {
		require.NoError(t, damast.CreateAdvisory(ctx, &damast.SecurityAdvisory{RepoID: other.ID + 1000, State: damast.AdvisoryClosed, CreatorID: reporter.ID, ReporterID: reporter.ID, Title: "t", Severity: "low"}))
	}
	MakeRequest(t, NewRequestWithJSON(t, "POST", "/api/v1/repos/"+other.FullName()+"/security-advisories/reports", map[string]any{
		"summary": "r", "description": "d", "severity": "low",
	}).AddTokenAuth(token), http.StatusTooManyRequests)
}

func TestDamastSecurityCollaboratorNeedsAccess(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&mailer.SendAsync, func(...*mailer.Message) {})()
	ctx := db.DefaultContext
	owner := forgery.CreateUser(t, nil)
	outsider := forgery.CreateUser(t, nil)
	repo := forgery.CreateRepository(t, owner, &forgery.CreateRepositoryOptions{IsPrivate: true})
	a, err := security.Create(ctx, repo, owner, &security.Input{Title: "t", Description: "d", Severity: "low"})
	require.NoError(t, err)

	link := "/" + repo.FullName() + "/security/advisories/" + a.AdvisoryID
	loginUser(t, owner.Name).MakeRequest(t, NewRequestWithValues(t, "POST", link+"/collaborators", map[string]string{"username": outsider.Name}), http.StatusSeeOther)
	MakeRequest(t, NewRequestWithJSON(t, "PATCH", "/api/v1/repos/"+repo.FullName()+"/security-advisories/"+a.AdvisoryID,
		map[string]any{"collaborating_users": []string{outsider.Name}}).AddTokenAuth(forgery.CreateAccessToken(t, owner, nil).Token), http.StatusUnprocessableEntity)
	ids, err := damast.AdvisoryCollaboratorIDs(ctx, a.ID)
	require.NoError(t, err)
	assert.Empty(t, ids)
	loginUser(t, outsider.Name).MakeRequest(t, NewRequest(t, "GET", link), http.StatusNotFound)

	// a collaborator who loses repository access loses the advisory too
	reader := forgery.CreateUser(t, nil)
	require.NoError(t, repo_module.AddCollaborator(ctx, repo, reader))
	require.NoError(t, repo_module.ChangeCollaborationAccessMode(ctx, repo, reader.ID, perm.AccessModeRead))
	require.NoError(t, security.AddCollaborator(ctx, repo, owner, a, reader))
	session := loginUser(t, reader.Name)
	session.MakeRequest(t, NewRequest(t, "GET", link), http.StatusOK)
	require.NoError(t, repo_module.DeleteCollaboration(ctx, repo, reader.ID))
	session.MakeRequest(t, NewRequest(t, "GET", link), http.StatusNotFound)
}

func TestDamastSecurityLifecycle(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&mailer.SendAsync, func(...*mailer.Message) {})()
	ctx := db.DefaultContext
	owner := forgery.CreateUser(t, nil)
	newOwner := forgery.CreateUser(t, nil)
	reporter := forgery.CreateUser(t, nil)
	repo := forgery.CreateRepository(t, owner, &forgery.CreateRepositoryOptions{SkipCleanup: true})
	require.NoError(t, damast.SetPrivateReporting(ctx, repo.ID, true))
	a, err := security.Report(ctx, repo, reporter, &security.Input{Title: "lifecycle", Description: "d", Severity: "low"})
	require.NoError(t, err)
	_, err = security.AddComment(ctx, repo, reporter, a, "from the reporter")
	require.NoError(t, err)

	// transfer: the advisory moves with the repository, its id stays
	require.NoError(t, repo_service.TransferOwnership(ctx, owner, newOwner, repo, nil))
	repo = unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
	link := fmt.Sprintf("/%s/%s/security/advisories/%s", newOwner.Name, repo.Name, a.AdvisoryID)
	loginUser(t, newOwner.Name).MakeRequest(t, NewRequest(t, "GET", link), http.StatusOK)
	loginUser(t, owner.Name).MakeRequest(t, NewRequest(t, "GET", link), http.StatusNotFound)

	// deleting the reporter keeps the report with a ghost user
	require.NoError(t, user_service.DeleteUser(ctx, reporter, false))
	a = unittest.AssertExistsAndLoadBean(t, &damast.SecurityAdvisory{ID: a.ID})
	assert.EqualValues(t, user_model.GhostUserID, a.ReporterID)
	assert.EqualValues(t, user_model.GhostUserID, a.CreatorID)
	page := loginUser(t, newOwner.Name).MakeRequest(t, NewRequest(t, "GET", link), http.StatusOK).Body.String()
	assert.Contains(t, page, "Ghost")
	assert.Contains(t, page, "from the reporter")

	// deleting the repository deletes its advisories, comments and settings
	require.NoError(t, repo_service.DeleteRepository(ctx, newOwner, repo, false))
	unittest.AssertNotExistsBean(t, &damast.SecurityAdvisory{ID: a.ID})
	unittest.AssertNotExistsBean(t, &damast.AdvisoryComment{AdvisoryID: a.ID})
	has, err := db.GetEngine(ctx).Where("repo_id = ?", repo.ID).Exist(new(damast.SecuritySetting))
	require.NoError(t, err)
	assert.False(t, has)
}

func TestDamastSecurityPolicy(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, _ *url.URL) {
		owner := forgery.CreateUser(t, nil)
		empty := forgery.CreateRepository(t, owner, nil)
		page := MakeRequest(t, NewRequest(t, "GET", "/"+empty.FullName()+"/security"), http.StatusOK).Body.String()
		assert.Contains(t, NewHTMLParser(t, bytes.NewBufferString(page)).Find("#damast-security-policy").Text(), "no security policy")
		tab := NewHTMLParser(t, bytes.NewBufferString(page)).Find(`.overflow-menu-items a[href$="/security"]`)
		assert.Equal(t, 1, tab.Length(), "Security tab in the header")

		// .forgejo, .gitea, .github, root, docs: the first one wins
		repo := forgery.CreateRepository(t, owner, &forgery.CreateRepositoryOptions{Files: forgery.MapFS{
			".github/SECURITY.md": forgery.MapFile("# Reporting\n\nMail **security@example.com**.\n"),
			"SECURITY.md":         forgery.MapFile("root"),
			"docs/SECURITY.md":    forgery.MapFile("docs"),
		}})
		doc := NewHTMLParser(t, MakeRequest(t, NewRequest(t, "GET", "/"+repo.FullName()+"/security"), http.StatusOK).Body)
		assert.Equal(t, "security@example.com", doc.Find("#damast-security-policy strong").Text())
		assert.Contains(t, doc.Find(".damast-security h4 a").First().Text(), ".github/SECURITY.md")
	})
}
