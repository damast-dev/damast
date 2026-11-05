// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"forgejo.org/models/damast"
	"forgejo.org/models/db"
	"forgejo.org/models/perm"
	user_model "forgejo.org/models/user"
	"forgejo.org/services/damast/security"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDamastSecurityWithdraw: a published advisory can be withdrawn (web and API); it stays public, marked
// as withdrawn, its OSV record gets "withdrawn", and withdrawn is final.
func TestDamastSecurityWithdraw(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	var box secMailbox
	defer box.mock()()
	ctx := db.DefaultContext
	owner := forgery.CreateUser(t, nil)
	collaborator := forgery.CreateUser(t, nil)
	repo := forgery.CreateRepository(t, owner, &forgery.CreateRepositoryOptions{Collaborators: map[*user_model.User]perm.AccessMode{collaborator: perm.AccessModeRead}})
	token := forgery.CreateAccessToken(t, owner, nil).Token
	api := "/api/v1/repos/" + repo.FullName() + "/security-advisories/"

	for _, viaAPI := range []bool{false, true} {
		a, err := security.Create(ctx, repo, owner, &security.Input{Title: fmt.Sprintf("withdraw api=%v", viaAPI), Description: "d", Severity: "low"})
		require.NoError(t, err)
		require.NoError(t, security.AddCollaborator(ctx, repo, owner, a, collaborator))
		link := "/" + repo.FullName() + "/security/advisories/" + a.AdvisoryID

		// only a published advisory can be withdrawn
		MakeRequest(t, NewRequestWithJSON(t, "PATCH", api+a.AdvisoryID, map[string]any{"state": "withdrawn"}).AddTokenAuth(token), http.StatusUnprocessableEntity)
		require.NoError(t, security.SetState(ctx, repo, owner, a, damast.AdvisoryPublished))
		box.take()

		if viaAPI {
			var r security.APIAdvisory
			DecodeJSON(t, MakeRequest(t, NewRequestWithJSON(t, "PATCH", api+a.AdvisoryID, map[string]any{"state": "withdrawn"}).AddTokenAuth(token), http.StatusOK), &r)
			assert.Equal(t, "withdrawn", r.State)
			assert.NotNil(t, r.WithdrawnAt)
		} else {
			session := loginUser(t, owner.Name)
			page := session.MakeRequest(t, NewRequest(t, "GET", link), http.StatusOK).Body.String()
			assert.Contains(t, page, `value="withdrawn"`, "published advisories offer the withdraw button")
			session.MakeRequest(t, NewRequestWithValues(t, "POST", link+"/state", map[string]string{"state": "withdrawn"}), http.StatusSeeOther)
		}
		assert.Equal(t, []string{collaborator.Name}, box.take(owner, collaborator), "participants learn about the withdrawal")

		a, err = damast.GetAdvisory(ctx, repo.ID, a.AdvisoryID)
		require.NoError(t, err)
		assert.Equal(t, damast.AdvisoryWithdrawn, a.State)
		assert.NotZero(t, a.WithdrawnUnix)

		// still public, marked as withdrawn, without the private discussion
		doc := NewHTMLParser(t, MakeRequest(t, NewRequest(t, "GET", link), http.StatusOK).Body)
		assert.Equal(t, 1, doc.Find("#damast-advisory-withdrawn").Length())
		assert.Equal(t, 0, doc.Find("#damast-advisory-private").Length())
		assert.Contains(t, MakeRequest(t, NewRequest(t, "GET", "/"+repo.FullName()+"/security"), http.StatusOK).Body.String(), a.AdvisoryID)

		var osv security.OSV
		DecodeJSON(t, MakeRequest(t, NewRequest(t, "GET", api+a.AdvisoryID+"/osv"), http.StatusOK), &osv)
		withdrawn, err := time.Parse(time.RFC3339, osv.Withdrawn)
		require.NoError(t, err)
		assert.WithinDuration(t, time.Now(), withdrawn, time.Minute)
		assert.Contains(t, MakeRequest(t, NewRequest(t, "GET", api+"osv"), http.StatusOK).Body.String(), `"withdrawn":"`)

		// final
		for _, state := range []string{"published", "draft", "closed"} {
			MakeRequest(t, NewRequestWithJSON(t, "PATCH", api+a.AdvisoryID, map[string]any{"state": state}).AddTokenAuth(token), http.StatusUnprocessableEntity)
		}
	}
}

// TestDamastSecurityPatchAtomic: PATCH applies content, collaborators and state all or nothing.
func TestDamastSecurityPatchAtomic(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	var box secMailbox
	defer box.mock()()
	ctx := db.DefaultContext
	owner := forgery.CreateUser(t, nil)
	reader := forgery.CreateUser(t, nil)
	// private, so a user without access is rejected as a collaborator
	repo := forgery.CreateRepository(t, owner, &forgery.CreateRepositoryOptions{IsPrivate: true, Collaborators: map[*user_model.User]perm.AccessMode{reader: perm.AccessModeRead}})
	token := forgery.CreateAccessToken(t, owner, nil).Token
	a, err := security.Create(ctx, repo, owner, &security.Input{Title: "before", Description: "d", Severity: "low"})
	require.NoError(t, err)
	url := "/api/v1/repos/" + repo.FullName() + "/security-advisories/" + a.AdvisoryID
	box.take()

	check := func() {
		t.Helper()
		got, err := damast.GetAdvisory(ctx, repo.ID, a.AdvisoryID)
		require.NoError(t, err)
		assert.Equal(t, "before", got.Title)
		assert.Equal(t, damast.AdvisoryDraft, got.State)
		ids, err := damast.AdvisoryCollaboratorIDs(ctx, a.ID)
		require.NoError(t, err)
		assert.Empty(t, ids)
		assert.Empty(t, box.take(owner, reader), "a failed PATCH notifies nobody")
	}

	// the last step (state) fails: content and collaborators are rolled back
	resp := MakeRequest(t, NewRequestWithJSON(t, "PATCH", url, map[string]any{
		"summary": "after", "collaborating_users": []string{reader.Name}, "state": "triage",
	}).AddTokenAuth(token), http.StatusUnprocessableEntity)
	assert.Contains(t, resp.Body.String(), "cannot become")
	check()

	// the middle step (collaborators) fails: content is rolled back
	MakeRequest(t, NewRequestWithJSON(t, "PATCH", url, map[string]any{
		"summary": "after", "collaborating_users": []string{forgery.CreateUser(t, nil).Name},
	}).AddTokenAuth(token), http.StatusUnprocessableEntity)
	check()

	// all steps succeed together, and notify after the commit
	MakeRequest(t, NewRequestWithJSON(t, "PATCH", url, map[string]any{
		"summary": "after", "collaborating_users": []string{reader.Name}, "state": "published",
	}).AddTokenAuth(token), http.StatusOK)
	got, err := damast.GetAdvisory(ctx, repo.ID, a.AdvisoryID)
	require.NoError(t, err)
	assert.Equal(t, "after", got.Title)
	assert.Equal(t, damast.AdvisoryPublished, got.State)
	ids, err := damast.AdvisoryCollaboratorIDs(ctx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, []int64{reader.ID}, ids)
	assert.Equal(t, []string{reader.Name, reader.Name}, box.take(owner, reader), "added + published")
}

// TestDamastSecurityCommentEdit: the poster edits and deletes their own comment; another participant cannot,
// an admin can. The matrix covers who gets 404.
func TestDamastSecurityCommentEdit(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	var box secMailbox
	defer box.mock()()
	ctx := db.DefaultContext
	owner := forgery.CreateUser(t, nil)
	reporter := forgery.CreateUser(t, nil)
	collaborator := forgery.CreateUser(t, nil)
	repo := forgery.CreateRepository(t, owner, &forgery.CreateRepositoryOptions{Collaborators: map[*user_model.User]perm.AccessMode{collaborator: perm.AccessModeRead}})
	require.NoError(t, damast.SetPrivateReporting(ctx, repo.ID, true))
	a, err := security.Report(ctx, repo, reporter, &security.Input{Title: "t", Description: "d", Severity: "low"})
	require.NoError(t, err)
	require.NoError(t, security.AddCollaborator(ctx, repo, owner, a, collaborator))
	c, err := security.AddComment(ctx, repo, reporter, a, "first version")
	require.NoError(t, err)
	box.take()
	link := fmt.Sprintf("/%s/security/advisories/%s/comments/%d", repo.FullName(), a.AdvisoryID, c.ID)
	api := fmt.Sprintf("/api/v1/repos/%s/security-advisories/%s/comments/%d", repo.FullName(), a.AdvisoryID, c.ID)

	reporterSession := loginUser(t, reporter.Name)
	page := reporterSession.MakeRequest(t, NewRequest(t, "GET", "/"+repo.FullName()+"/security/advisories/"+a.AdvisoryID), http.StatusOK).Body.String()
	assert.Contains(t, page, link+"/edit", "the poster gets the edit form")
	page = loginUser(t, collaborator.Name).MakeRequest(t, NewRequest(t, "GET", "/"+repo.FullName()+"/security/advisories/"+a.AdvisoryID), http.StatusOK).Body.String()
	assert.NotContains(t, page, link+"/edit", "other participants do not")

	reporterSession.MakeRequest(t, NewRequestWithValues(t, "POST", link+"/edit", map[string]string{"content": "second version"}), http.StatusSeeOther)
	got, err := damast.GetAdvisoryComment(ctx, a.ID, c.ID)
	require.NoError(t, err)
	assert.Equal(t, "second version", got.Content)
	MakeRequest(t, NewRequestWithJSON(t, "PATCH", api, map[string]any{"body": "pwned"}).AddTokenAuth(forgery.CreateAccessToken(t, collaborator, nil).Token), http.StatusNotFound)

	var r security.APIComment
	DecodeJSON(t, MakeRequest(t, NewRequestWithJSON(t, "PATCH", api, map[string]any{"body": "third version"}).AddTokenAuth(forgery.CreateAccessToken(t, reporter, nil).Token), http.StatusOK), &r)
	assert.Equal(t, "third version", r.Body)
	assert.Empty(t, box.take(owner, reporter, collaborator), "edits notify nobody")

	// an admin may delete any comment
	MakeRequest(t, NewRequest(t, "DELETE", api).AddTokenAuth(forgery.CreateAccessToken(t, owner, nil).Token), http.StatusNoContent)
	_, err = damast.GetAdvisoryComment(ctx, a.ID, c.ID)
	require.Error(t, err)
	MakeRequest(t, NewRequest(t, "DELETE", api).AddTokenAuth(forgery.CreateAccessToken(t, owner, nil).Token), http.StatusNotFound)

	// a comment id of another advisory is a 404 even for admins
	other, err := security.Create(ctx, repo, owner, &security.Input{Title: "t2", Description: "d", Severity: "low"})
	require.NoError(t, err)
	c2, err := security.AddComment(ctx, repo, owner, other, "other advisory")
	require.NoError(t, err)
	wrong := strings.Replace(api, fmt.Sprint(c.ID), fmt.Sprint(c2.ID), 1)
	MakeRequest(t, NewRequestWithJSON(t, "PATCH", wrong, map[string]any{"body": "x"}).AddTokenAuth(forgery.CreateAccessToken(t, owner, nil).Token), http.StatusNotFound)
}
