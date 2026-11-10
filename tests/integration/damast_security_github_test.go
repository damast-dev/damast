// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"forgejo.org/models/damast"
	"forgejo.org/models/db"
	"forgejo.org/models/perm"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/json"
	"forgejo.org/modules/test"
	"forgejo.org/services/damast/security"
	"forgejo.org/services/mailer"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// GitHub parity of the REST API: pagination, the private reporting switch, validation and response fields.

// TestDamastSecurityPagination: lists take page and per_page (or Forgejo's limit) and send X-Total-Count and Link.
func TestDamastSecurityPagination(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&mailer.SendAsync, func(...*mailer.Message) {})()
	ctx := db.DefaultContext
	owner := forgery.CreateUser(t, nil)
	repo := forgery.CreateRepository(t, owner, nil)
	token := forgery.CreateAccessToken(t, owner, nil).Token
	var ids []string
	for i := range 5 {
		a, err := security.Create(ctx, repo, owner, &security.Input{Title: fmt.Sprintf("a%d", i), Description: "d", Severity: "low"})
		require.NoError(t, err)
		ids = append(ids, a.AdvisoryID)
		if i == 0 {
			for j := range 3 {
				_, err := security.AddComment(ctx, repo, owner, a, fmt.Sprintf("c%d", j))
				require.NoError(t, err)
			}
		}
	}
	api := "/api/v1/repos/" + repo.FullName() + "/security-advisories"

	for _, q := range []string{"?per_page=2&page=2", "?limit=2&page=2"} {
		resp := MakeRequest(t, NewRequest(t, "GET", api+q).AddTokenAuth(token), http.StatusOK)
		var list []security.APIAdvisory
		DecodeJSON(t, resp, &list)
		require.Len(t, list, 2, q)
		assert.Equal(t, []string{ids[2], ids[1]}, []string{list[0].GhsaID, list[1].GhsaID}, "newest first, second page: %s", q)
		assert.Equal(t, "5", resp.Header().Get("X-Total-Count"), q)
		link := resp.Header().Get("Link")
		assert.Contains(t, link, `rel="next"`, q)
		assert.Contains(t, link, `rel="prev"`, q)
		assert.Contains(t, link, `rel="last"`, q)
	}
	// the state filter counts before paging
	resp := MakeRequest(t, NewRequest(t, "GET", api+"?state=draft&per_page=1").AddTokenAuth(token), http.StatusOK)
	assert.Equal(t, "5", resp.Header().Get("X-Total-Count"))

	resp = MakeRequest(t, NewRequest(t, "GET", api+"/"+ids[0]+"/comments?per_page=2&page=2").AddTokenAuth(token), http.StatusOK)
	var comments []security.APIComment
	DecodeJSON(t, resp, &comments)
	require.Len(t, comments, 1)
	assert.Equal(t, "c2", comments[0].Body)
	assert.Equal(t, "3", resp.Header().Get("X-Total-Count"))
}

// TestDamastSecurityReportingAPI: GET/PUT/DELETE /private-vulnerability-reporting like GitHub, for admins only.
func TestDamastSecurityReportingAPI(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	owner := forgery.CreateUser(t, nil)
	reader := forgery.CreateUser(t, nil)
	repo := forgery.CreateRepository(t, owner, &forgery.CreateRepositoryOptions{Collaborators: map[*user_model.User]perm.AccessMode{reader: perm.AccessModeWrite}})
	token := forgery.CreateAccessToken(t, owner, nil).Token
	readerToken := forgery.CreateAccessToken(t, reader, nil).Token
	url := "/api/v1/repos/" + repo.FullName() + "/private-vulnerability-reporting"
	enabled := func() bool {
		t.Helper()
		var r struct {
			Enabled bool `json:"enabled"`
		}
		DecodeJSON(t, MakeRequest(t, NewRequest(t, "GET", url).AddTokenAuth(token), http.StatusOK), &r)
		return r.Enabled
	}

	assert.False(t, enabled())
	MakeRequest(t, NewRequest(t, "PUT", url).AddTokenAuth(token), http.StatusNoContent)
	assert.True(t, enabled())
	on, err := damast.PrivateReportingEnabled(db.DefaultContext, repo)
	require.NoError(t, err)
	assert.True(t, on)
	MakeRequest(t, NewRequest(t, "PUT", url).AddTokenAuth(token), http.StatusNoContent) // idempotent

	for _, m := range []string{"GET", "PUT", "DELETE"} {
		MakeRequest(t, NewRequest(t, m, url).AddTokenAuth(readerToken), http.StatusNotFound)
		MakeRequest(t, NewRequest(t, m, url), http.StatusNotFound)
	}
	assert.True(t, enabled(), "denied requests change nothing")

	MakeRequest(t, NewRequest(t, "DELETE", url).AddTokenAuth(token), http.StatusNoContent)
	assert.False(t, enabled())

	// a read-only token may read the switch but not flip it
	readOnly := forgery.CreateAccessToken(t, owner, &forgery.CreateAccessTokenOptions{Scope: "read:repository"}).Token
	MakeRequest(t, NewRequest(t, "GET", url).AddTokenAuth(readOnly), http.StatusOK)
	MakeRequest(t, NewRequest(t, "PUT", url).AddTokenAuth(readOnly), http.StatusForbidden)
}

// TestDamastSecurityValidation: unknown states are a 422 with a readable message; cve_id null clears the CVE.
func TestDamastSecurityValidation(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	owner := forgery.CreateUser(t, nil)
	repo := forgery.CreateRepository(t, owner, nil)
	token := forgery.CreateAccessToken(t, owner, nil).Token
	a, err := security.Create(db.DefaultContext, repo, owner, &security.Input{Title: "t", Description: "d", Severity: "low", CVEID: "CVE-2026-1234"})
	require.NoError(t, err)
	api := "/api/v1/repos/" + repo.FullName() + "/security-advisories"

	resp := MakeRequest(t, NewRequest(t, "GET", api+"?state=bogus").AddTokenAuth(token), http.StatusUnprocessableEntity)
	assert.Contains(t, resp.Body.String(), "triage, draft, published, closed or withdrawn")
	MakeRequest(t, NewRequest(t, "GET", api+"?state=published").AddTokenAuth(token), http.StatusOK)

	for _, state := range []string{"", "bogus"} {
		resp := MakeRequest(t, NewRequestWithJSON(t, "PATCH", api+"/"+a.AdvisoryID, map[string]any{"state": state}).AddTokenAuth(token), http.StatusUnprocessableEntity)
		assert.NotContains(t, resp.Body.String(), "become .", state)
		assert.Contains(t, resp.Body.String(), "triage, draft, published, closed or withdrawn", state)
	}

	// missing: unchanged; null: cleared (GitHub)
	var r security.APIAdvisory
	DecodeJSON(t, MakeRequest(t, NewRequestWithJSON(t, "PATCH", api+"/"+a.AdvisoryID, map[string]any{"summary": "t2"}).AddTokenAuth(token), http.StatusOK), &r)
	require.NotNil(t, r.CveID)
	assert.Equal(t, "CVE-2026-1234", *r.CveID)
	req := NewRequestWithBody(t, "PATCH", api+"/"+a.AdvisoryID, strings.NewReader(`{"cve_id": null}`)).AddTokenAuth(token)
	req.Header.Set("Content-Type", "application/json")
	r = security.APIAdvisory{}
	DecodeJSON(t, MakeRequest(t, req, http.StatusOK), &r)
	assert.Nil(t, r.CveID)
	got, err := damast.GetAdvisory(db.DefaultContext, repo.ID, a.AdvisoryID)
	require.NoError(t, err)
	assert.Empty(t, got.CVEID)
}

// TestDamastSecurityGitHubFields: cwes, publisher, submission, cvss_severities and credits_detailed.
func TestDamastSecurityGitHubFields(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&mailer.SendAsync, func(...*mailer.Message) {})()
	ctx := db.DefaultContext
	owner := forgery.CreateUser(t, nil)
	reporter := forgery.CreateUser(t, nil)
	repo := forgery.CreateRepository(t, owner, nil)
	token := forgery.CreateAccessToken(t, owner, nil).Token
	require.NoError(t, damast.SetPrivateReporting(ctx, repo, true))
	api := "/api/v1/repos/" + repo.FullName() + "/security-advisories/"
	get := func(id string) map[string]any {
		t.Helper()
		var m map[string]any
		require.NoError(t, json.Unmarshal(MakeRequest(t, NewRequest(t, "GET", api+id).AddTokenAuth(token), http.StatusOK).Body.Bytes(), &m))
		return m
	}

	a, err := security.Create(ctx, repo, owner, &security.Input{
		Title: "t", Description: "d", CVSSVector: "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", CWEIDs: []string{"CWE-79"},
		Credits: []damast.Credit{{UserID: reporter.ID, Type: "finder"}},
	})
	require.NoError(t, err)
	m := get(a.AdvisoryID)
	assert.Equal(t, []any{map[string]any{"cwe_id": "CWE-79", "name": ""}}, m["cwes"])
	assert.Nil(t, m["publisher"])
	assert.Nil(t, m["submission"], "not a private report")
	sev := m["cvss_severities"].(map[string]any)
	assert.Equal(t, map[string]any{"vector_string": "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H", "score": 9.8}, sev["cvss_v3"])
	assert.Contains(t, sev, "cvss_v4")
	assert.Nil(t, sev["cvss_v4"])
	detailed := m["credits_detailed"].([]any)
	require.Len(t, detailed, 1)
	d := detailed[0].(map[string]any)
	assert.Equal(t, reporter.Name, d["user"].(map[string]any)["login"])
	assert.Equal(t, "finder", d["type"])
	assert.Equal(t, "accepted", d["state"])

	require.NoError(t, security.SetState(ctx, repo, owner, a, damast.AdvisoryPublished))
	assert.Equal(t, owner.Name, get(a.AdvisoryID)["publisher"].(map[string]any)["login"])

	// without CVSS: both null
	b, err := security.Create(ctx, repo, owner, &security.Input{Title: "t", Description: "d", Severity: "low"})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"cvss_v3": nil, "cvss_v4": nil}, get(b.AdvisoryID)["cvss_severities"])
	assert.Equal(t, []any{}, get(b.AdvisoryID)["cwes"])

	r, err := security.Report(ctx, repo, reporter, &security.Input{Title: "r", Description: "d", Severity: "low"})
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"accepted": false}, get(r.AdvisoryID)["submission"])
	require.NoError(t, security.SetState(ctx, repo, owner, r, damast.AdvisoryDraft))
	assert.Equal(t, map[string]any{"accepted": true}, get(r.AdvisoryID)["submission"])
	require.NoError(t, security.SetState(ctx, repo, owner, r, damast.AdvisoryClosed))
	assert.Equal(t, map[string]any{"accepted": true}, get(r.AdvisoryID)["submission"], "closing later does not undo the acceptance")
}

// TestDamastSecurityCreditTypes: the credit type is shown in the user's language.
func TestDamastSecurityCreditTypes(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := db.DefaultContext
	owner := forgery.CreateUser(t, nil)
	helper := forgery.CreateUser(t, nil)
	repo := forgery.CreateRepository(t, owner, nil)
	a, err := security.Create(ctx, repo, owner, &security.Input{Title: "t", Description: "d", Severity: "low", Credits: []damast.Credit{{UserID: helper.ID, Type: "remediation_developer"}}})
	require.NoError(t, err)
	require.NoError(t, security.SetState(ctx, repo, owner, a, damast.AdvisoryPublished))
	link := "/" + repo.FullName() + "/security/advisories/" + a.AdvisoryID
	page := MakeRequest(t, NewRequest(t, "GET", link), http.StatusOK).Body.String()
	assert.Contains(t, page, "Remediation developer")
	assert.NotContains(t, page, "remediation_developer")
	req := NewRequest(t, "GET", link)
	req.Header.Set("Accept-Language", "de-DE")
	assert.Contains(t, MakeRequest(t, req, http.StatusOK).Body.String(), "Entwicklung der Behebung")
}

// TestDamastSecurityArchived: an archived repository's advisories can be read but not changed.
func TestDamastSecurityArchived(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	var box secMailbox
	defer box.mock()()
	ctx := db.DefaultContext
	owner := forgery.CreateUser(t, nil)
	repo := forgery.CreateRepository(t, owner, nil)
	token := forgery.CreateAccessToken(t, owner, nil).Token
	require.NoError(t, damast.SetPrivateReporting(ctx, repo, true))
	a, err := security.Create(ctx, repo, owner, &security.Input{Title: "t", Description: "d", Severity: "low"})
	require.NoError(t, err)
	c, err := security.AddComment(ctx, repo, owner, a, "hello")
	require.NoError(t, err)
	require.NoError(t, repo_model.SetArchiveRepoState(ctx, repo, true))
	before := secFingerprint(t)
	box.take()

	session := loginUser(t, owner.Name)
	web := "/" + repo.FullName() + "/security"
	link := web + "/advisories/" + a.AdvisoryID
	page := session.MakeRequest(t, NewRequest(t, "GET", link), http.StatusOK).Body.String()
	assert.NotContains(t, page, link+"/edit", "no edit button")
	assert.NotContains(t, page, link+"/state", "no state buttons")
	assert.NotContains(t, page, link+"/comments\"", "no comment form")
	tab := session.MakeRequest(t, NewRequest(t, "GET", web), http.StatusOK).Body.String()
	assert.NotContains(t, tab, web+"/advisories/new")
	assert.NotContains(t, tab, web+"/settings")
	for _, p := range []string{web + "/advisories/new", link + "/edit"} {
		session.MakeRequest(t, NewRequest(t, "GET", p), http.StatusNotFound)
	}
	for _, p := range []string{
		web + "/settings", web + "/advisories/new", link + "/edit", link + "/state", link + "/comments",
		fmt.Sprintf("%s/comments/%d/edit", link, c.ID), fmt.Sprintf("%s/comments/%d/delete", link, c.ID),
		link + "/collaborators", link + "/collaborators/remove",
	} {
		session.MakeRequest(t, NewRequestWithValues(t, "POST", p, map[string]string{
			"title": "x", "description": "x", "severity": "low", "state": "published", "content": "x", "username": owner.Name,
		}), http.StatusNotFound)
	}

	api := "/api/v1/repos/" + repo.FullName()
	adv := api + "/security-advisories/" + a.AdvisoryID
	MakeRequest(t, NewRequest(t, "GET", adv).AddTokenAuth(token), http.StatusOK)
	MakeRequest(t, NewRequest(t, "GET", adv+"/comments").AddTokenAuth(token), http.StatusOK)
	MakeRequest(t, NewRequest(t, "GET", api+"/private-vulnerability-reporting").AddTokenAuth(token), http.StatusOK)
	for _, r := range []struct {
		method, path string
		body         any
	}{
		{"POST", api + "/security-advisories", map[string]any{"summary": "x", "description": "x", "severity": "low"}},
		{"PATCH", adv, map[string]any{"state": "published"}},
		{"POST", adv + "/comments", map[string]any{"body": "x"}},
		{"PATCH", fmt.Sprintf("%s/comments/%d", adv, c.ID), map[string]any{"body": "x"}},
		{"DELETE", fmt.Sprintf("%s/comments/%d", adv, c.ID), nil},
		{"PUT", api + "/private-vulnerability-reporting", nil},
		{"DELETE", api + "/private-vulnerability-reporting", nil},
	} {
		req := NewRequest(t, r.method, r.path)
		if r.body != nil {
			req = NewRequestWithJSON(t, r.method, r.path, r.body)
		}
		MakeRequest(t, req.AddTokenAuth(token), http.StatusLocked)
	}
	// reporting is off in archived repositories, as before
	MakeRequest(t, NewRequestWithJSON(t, "POST", api+"/security-advisories/reports", map[string]any{"summary": "x", "description": "x", "severity": "low"}).AddTokenAuth(forgery.CreateAccessToken(t, forgery.CreateUser(t, nil), nil).Token), http.StatusNotFound)

	assert.Equal(t, before, secFingerprint(t), "nothing changed")
	assert.Empty(t, box.take(owner))
}
