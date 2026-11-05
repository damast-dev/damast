// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"net/http"
	"testing"

	"forgejo.org/models/damast"
	"forgejo.org/models/db"
	"forgejo.org/models/unittest"
	"forgejo.org/modules/test"
	"forgejo.org/services/damast/security"
	"forgejo.org/services/mailer"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A public-only token is limited to public data. Drafts, reports and private discussions in a public repository
// are not public, so such a token gets exactly what an anonymous visitor gets, even for the repository's owner.
func TestDamastSecurityPublicOnlyToken(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&mailer.SendAsync, func(...*mailer.Message) {})()
	ctx := db.DefaultContext
	owner := forgery.CreateUser(t, nil)
	repo := forgery.CreateRepository(t, owner, &forgery.CreateRepositoryOptions{Files: forgery.FilesInit{}})
	require.NoError(t, damast.SetPrivateReporting(ctx, repo.ID, true))
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
