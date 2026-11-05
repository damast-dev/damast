// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strings"
	"testing"

	"forgejo.org/models/damast"
	"forgejo.org/models/db"
	"forgejo.org/models/perm"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/test"
	"forgejo.org/modules/util"
	"forgejo.org/services/damast/security"
	"forgejo.org/services/mailer"
	"forgejo.org/tests"
	"forgejo.org/tests/forgery"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The visibility matrix: every Security route (web and API) x every role x every advisory state x private and
// public repositories. TestDamastSecurityRoutesCovered fails when a route is registered without a row here.

var (
	secRoles  = []string{"anonymous", "stranger", "reader", "writer", "admin_team", "admin_collab", "org_owner", "site_admin", "reporter", "collaborator"}
	secStates = []string{damast.AdvisoryTriage, damast.AdvisoryDraft, damast.AdvisoryPublished, damast.AdvisoryClosed, damast.AdvisoryWithdrawn}
	// roles with admin access: admin team member, admin collaborator, org owner, site admin
	secAdmins = []string{"admin_team", "admin_collab", "org_owner", "site_admin"}
	// everyone who may see an unpublished advisory
	secPrivate = append(slices.Clone(secAdmins), "reporter", "collaborator")
	// who may edit or delete the matrix's discussion comment: admins and its poster, the collaborator
	secCommentEditors = append(slices.Clone(secAdmins), "collaborator")
)

// secPublic reports whether an advisory in the state is public (published, or published and withdrawn).
func secPublic(state string) bool {
	return state == damast.AdvisoryPublished || state == damast.AdvisoryWithdrawn
}

type secRoleSet []string

// readers are the roles that can read the repository's code.
func secReaders(private bool) secRoleSet {
	r := append(slices.Clone(secPrivate), "reader", "writer")
	if !private {
		r = append(r, "stranger", "anonymous")
	}
	return r
}

func secSignedReaders(private bool) secRoleSet {
	return slices.DeleteFunc(secReaders(private), func(r string) bool { return r == "anonymous" })
}

// secCanView: who may see the advisory (and its title) in that state.
func secCanView(private bool, state string) secRoleSet {
	if secPublic(state) {
		return secReaders(private)
	}
	return secPrivate
}

type secRoute struct {
	method  string
	pattern string // as registered, with {username}/{reponame} and {id} or {ghsa_id}
	allow   func(private bool, state string) secRoleSet
	ok      int                    // status for allowed roles with the noop payload
	attack  func(state string) any // a valid, state changing payload sent by every denied role
	noop    any                    // payload for allowed roles; must not change anything
	// throwaway: allowed roles act on a fresh comment instead of the matrix's one (for deletes, which
	// change something even with an empty payload); the test checks it is gone afterwards
	throwaway bool
}

func (r secRoute) api() bool { return strings.HasPrefix(r.pattern, "/api/") }
func (r secRoute) advisory() bool {
	return strings.Contains(r.pattern, "{id}") || strings.Contains(r.pattern, "{ghsa_id}")
}

func secForm(kv ...string) url.Values {
	v := url.Values{}
	for i := 0; i < len(kv); i += 2 {
		v.Add(kv[i], kv[i+1])
	}
	return v
}

func secValidForm() url.Values {
	return secForm("title", "pwned", "description", "pwned", "severity", "high", "ecosystem", "npm", "package", "x", "vulnerable", "< 1")
}

var secValidJSON = map[string]any{"summary": "pwned", "description": "pwned", "severity": "high"}

// secNextState is a valid transition, so a denied request would change something if it got through.
// Withdrawn is final; "draft" stands in for it.
func secNextState(state string) string {
	switch state {
	case damast.AdvisoryTriage, damast.AdvisoryClosed, damast.AdvisoryWithdrawn:
		return damast.AdvisoryDraft
	case damast.AdvisoryPublished:
		return damast.AdvisoryWithdrawn
	}
	return damast.AdvisoryClosed
}

func secAll(set secRoleSet) func(bool, string) secRoleSet {
	return func(bool, string) secRoleSet { return set }
}

var secRoutes = []secRoute{
	// web
	{method: "GET", pattern: "/{username}/{reponame}/security", allow: func(p bool, _ string) secRoleSet { return secReaders(p) }, ok: 200},
	{
		method: "POST", pattern: "/{username}/{reponame}/security/settings", allow: secAll(secAdmins), ok: 303,
		attack: func(string) any { return secForm() }, noop: secForm("private_reporting", "on"),
	},
	{method: "GET", pattern: "/{username}/{reponame}/security/report", allow: func(p bool, _ string) secRoleSet { return secSignedReaders(p) }, ok: 200},
	{
		method: "POST", pattern: "/{username}/{reponame}/security/report", allow: func(p bool, _ string) secRoleSet { return secSignedReaders(p) }, ok: 200,
		attack: func(string) any { return secValidForm() }, noop: secForm("title", ""),
	},
	{method: "GET", pattern: "/{username}/{reponame}/security/advisories/new", allow: secAll(secAdmins), ok: 200},
	{
		method: "POST", pattern: "/{username}/{reponame}/security/advisories/new", allow: secAll(secAdmins), ok: 200,
		attack: func(string) any { return secValidForm() }, noop: secForm("title", ""),
	},
	{method: "GET", pattern: "/{username}/{reponame}/security/advisories/{id}", allow: secCanView, ok: 200},
	{method: "GET", pattern: "/{username}/{reponame}/security/advisories/{id}/edit", allow: secAll(secAdmins), ok: 200},
	{
		method: "POST", pattern: "/{username}/{reponame}/security/advisories/{id}/edit", allow: secAll(secAdmins), ok: 200,
		attack: func(string) any { return secValidForm() }, noop: secForm("title", ""),
	},
	{
		method: "POST", pattern: "/{username}/{reponame}/security/advisories/{id}/state", allow: secAll(secAdmins), ok: 303,
		attack: func(s string) any { return secForm("state", secNextState(s)) }, noop: secForm("state", "bogus"),
	},
	{
		method: "POST", pattern: "/{username}/{reponame}/security/advisories/{id}/comments", allow: secAll(secPrivate), ok: 303,
		attack: func(string) any { return secForm("content", "pwned") }, noop: secForm("content", ""),
	},
	{
		method: "POST", pattern: "/{username}/{reponame}/security/advisories/{id}/comments/{comment_id}/edit", allow: secAll(secCommentEditors), ok: 303,
		attack: func(string) any { return secForm("content", "pwned") }, noop: secForm("content", ""),
	},
	{
		method: "POST", pattern: "/{username}/{reponame}/security/advisories/{id}/comments/{comment_id}/delete", allow: secAll(secCommentEditors), ok: 303,
		attack: func(string) any { return secForm() }, noop: secForm(), throwaway: true,
	},
	{
		method: "POST", pattern: "/{username}/{reponame}/security/advisories/{id}/collaborators", allow: secAll(secAdmins), ok: 303,
		attack: func(string) any { return secForm("username", "STRANGER") }, noop: secForm("username", "no-such-user"),
	},
	{
		method: "POST", pattern: "/{username}/{reponame}/security/advisories/{id}/collaborators/remove", allow: secAll(secAdmins), ok: 303,
		attack: func(string) any { return secForm("username", "COLLABORATOR") }, noop: secForm("username", "no-such-user"),
	},

	// API
	{method: "GET", pattern: "/api/v1/repos/{username}/{reponame}/security-advisories", allow: func(p bool, _ string) secRoleSet { return secReaders(p) }, ok: 200},
	{
		method: "POST", pattern: "/api/v1/repos/{username}/{reponame}/security-advisories", allow: secAll(secAdmins), ok: 422,
		attack: func(string) any { return secValidJSON }, noop: map[string]any{"summary": ""},
	},
	{
		method: "POST", pattern: "/api/v1/repos/{username}/{reponame}/security-advisories/reports", allow: func(p bool, _ string) secRoleSet { return secSignedReaders(p) }, ok: 422,
		attack: func(string) any { return secValidJSON }, noop: map[string]any{"summary": ""},
	},
	{method: "GET", pattern: "/api/v1/repos/{username}/{reponame}/security-advisories/osv", allow: func(p bool, _ string) secRoleSet { return secReaders(p) }, ok: 200},
	{method: "GET", pattern: "/api/v1/repos/{username}/{reponame}/security-advisories/{ghsa_id}", allow: secCanView, ok: 200},
	{
		method: "PATCH", pattern: "/api/v1/repos/{username}/{reponame}/security-advisories/{ghsa_id}", allow: secAll(secAdmins), ok: 422,
		attack: func(s string) any {
			return map[string]any{"summary": "pwned", "state": secNextState(s), "collaborating_users": []string{}}
		},
		noop: map[string]any{"state": "bogus"},
	},
	{
		method: "GET", pattern: "/api/v1/repos/{username}/{reponame}/security-advisories/{ghsa_id}/osv", ok: 200,
		allow: func(p bool, s string) secRoleSet { // unpublished advisories have no OSV document, not even for admins
			if secPublic(s) {
				return secReaders(p)
			}
			return nil
		},
	},
	{method: "GET", pattern: "/api/v1/repos/{username}/{reponame}/security-advisories/{ghsa_id}/comments", allow: secAll(secPrivate), ok: 200},
	{
		method: "POST", pattern: "/api/v1/repos/{username}/{reponame}/security-advisories/{ghsa_id}/comments", allow: secAll(secPrivate), ok: 422,
		attack: func(string) any { return map[string]any{"body": "pwned"} }, noop: map[string]any{"body": ""},
	},
	{
		method: "PATCH", pattern: "/api/v1/repos/{username}/{reponame}/security-advisories/{ghsa_id}/comments/{comment_id}", allow: secAll(secCommentEditors), ok: 422,
		attack: func(string) any { return map[string]any{"body": "pwned"} }, noop: map[string]any{"body": ""},
	},
	{
		method: "DELETE", pattern: "/api/v1/repos/{username}/{reponame}/security-advisories/{ghsa_id}/comments/{comment_id}", allow: secAll(secCommentEditors), ok: 204,
		throwaway: true,
	},
}

type secEnv struct {
	users      map[string]*user_model.User // nil for anonymous
	sessions   map[string]*TestSession
	tokens     map[string]string
	repos      map[bool]*repo_model.Repository
	advisories map[bool]map[string]*damast.SecurityAdvisory
	comments   map[int64]string // advisory id -> marker in its private discussion
	commentIDs map[int64]int64  // advisory id -> id of that comment (posted by "collaborator")
}

// prepareSecurityEnv creates an organization with a private and a public repository, a user for every role and
// one advisory per state in each repository, all reported by "reporter", with "collaborator" added and a comment.
func prepareSecurityEnv(t *testing.T) *secEnv {
	t.Helper()
	ctx := db.DefaultContext
	e := &secEnv{
		users: map[string]*user_model.User{}, sessions: map[string]*TestSession{}, tokens: map[string]string{},
		repos: map[bool]*repo_model.Repository{}, advisories: map[bool]map[string]*damast.SecurityAdvisory{}, comments: map[int64]string{}, commentIDs: map[int64]int64{},
	}
	for _, role := range secRoles {
		switch role {
		case "anonymous":
			continue
		case "site_admin":
			e.users[role] = unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1}) // fixture admin
		default:
			e.users[role] = forgery.CreateUser(t, nil)
		}
	}
	org := forgery.CreateOrganisation(t, e.users["org_owner"])
	forgery.CreateTeam(t, org, &forgery.CreateTeamOptions{Mode: perm.AccessModeAdmin, Members: []*user_model.User{e.users["admin_team"]}})
	admin := e.users["admin_team"]
	for _, private := range []bool{true, false} {
		repo := forgery.CreateRepository(t, org.AsUser(), &forgery.CreateRepositoryOptions{
			IsPrivate: private, Files: forgery.FilesInit{},
			Collaborators: map[*user_model.User]perm.AccessMode{
				e.users["admin_collab"]: perm.AccessModeAdmin, e.users["writer"]: perm.AccessModeWrite, e.users["reader"]: perm.AccessModeRead,
				e.users["reporter"]: perm.AccessModeRead, e.users["collaborator"]: perm.AccessModeRead,
			},
		})
		e.repos[private] = repo
		require.NoError(t, damast.SetPrivateReporting(ctx, repo.ID, true))
		e.advisories[private] = map[string]*damast.SecurityAdvisory{}
		for _, state := range secStates {
			a, err := security.Report(ctx, repo, e.users["reporter"], &security.Input{
				Title: "ADVTITLE-" + damast.NewAdvisoryID(), Description: "secret details", Severity: "high",
			})
			require.NoError(t, err)
			switch state {
			case damast.AdvisoryDraft:
				require.NoError(t, security.SetState(ctx, repo, admin, a, damast.AdvisoryDraft))
			case damast.AdvisoryPublished:
				require.NoError(t, security.SetState(ctx, repo, admin, a, damast.AdvisoryDraft))
				require.NoError(t, security.SetState(ctx, repo, admin, a, damast.AdvisoryPublished))
			case damast.AdvisoryClosed:
				require.NoError(t, security.SetState(ctx, repo, admin, a, damast.AdvisoryClosed))
			case damast.AdvisoryWithdrawn:
				require.NoError(t, security.SetState(ctx, repo, admin, a, damast.AdvisoryDraft))
				require.NoError(t, security.SetState(ctx, repo, admin, a, damast.AdvisoryPublished))
				require.NoError(t, security.SetState(ctx, repo, admin, a, damast.AdvisoryWithdrawn))
			}
			require.NoError(t, security.AddCollaborator(ctx, repo, admin, a, e.users["collaborator"]))
			marker := "ADVCOMMENT-" + damast.NewAdvisoryID()
			c, err := security.AddComment(ctx, repo, e.users["collaborator"], a, marker)
			require.NoError(t, err)
			e.comments[a.ID] = marker
			e.commentIDs[a.ID] = c.ID
			e.advisories[private][state] = a
		}
	}
	for role, u := range e.users {
		e.sessions[role] = loginUser(t, u.Name)
		e.tokens[role] = forgery.CreateAccessToken(t, u, nil).Token
	}
	return e
}

func (e *secEnv) path(r secRoute, repo *repo_model.Repository, a *damast.SecurityAdvisory, commentID int64) string {
	p := strings.ReplaceAll(r.pattern, "{username}/{reponame}", repo.OwnerName+"/"+repo.Name)
	if a != nil {
		p = strings.NewReplacer("{id}", a.AdvisoryID, "{ghsa_id}", a.AdvisoryID, "{comment_id}", fmt.Sprint(commentID)).Replace(p)
	}
	return p
}

func (e *secEnv) payload(body any) any {
	if v, ok := body.(url.Values); ok {
		out := url.Values{}
		for k, vs := range v {
			for _, s := range vs {
				out.Add(k, strings.NewReplacer("STRANGER", e.users["stranger"].Name, "COLLABORATOR", e.users["collaborator"].Name).Replace(s))
			}
		}
		return out
	}
	return body
}

func (e *secEnv) do(t *testing.T, role, method, path string, api bool, body any) *httptest.ResponseRecorder {
	t.Helper()
	var req *RequestWrapper
	switch b := e.payload(body).(type) {
	case nil:
		req = NewRequest(t, method, path)
	case url.Values:
		req = NewRequestWithURLValues(t, method, path, b)
	default:
		req = NewRequestWithJSON(t, method, path, b)
	}
	if api {
		if role != "anonymous" {
			req.AddTokenAuth(e.tokens[role])
		}
		return MakeRequest(t, req, NoExpectedStatus)
	}
	req.SetHeader("Accept", "text/html")
	if role == "anonymous" {
		return MakeRequest(t, req, NoExpectedStatus)
	}
	return e.sessions[role].MakeRequest(t, req, NoExpectedStatus)
}

// secFingerprint is all Damast security data, to prove that nothing changed.
func secFingerprint(t *testing.T) string {
	var a []damast.SecurityAdvisory
	var c []damast.AdvisoryComment
	var col []damast.AdvisoryCollaborator
	var s []damast.SecuritySetting
	e := db.GetEngine(db.DefaultContext)
	require.NoError(t, e.OrderBy("id").Find(&a))
	require.NoError(t, e.OrderBy("id").Find(&c))
	require.NoError(t, e.OrderBy("id").Find(&col))
	require.NoError(t, e.OrderBy("repo_id").Find(&s))
	b, err := json.Marshal([]any{a, c, col, s})
	require.NoError(t, err)
	return string(b)
}

func TestDamastSecurityMatrix(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	var mails []*mailer.Message
	defer test.MockVariableValue(&mailer.SendAsync, func(msgs ...*mailer.Message) { mails = append(mails, msgs...) })()
	e := prepareSecurityEnv(t)
	mails = nil
	before := secFingerprint(t)

	type cell struct{ route, state string }
	summary := map[cell]map[bool][]string{} // allowed roles per cell and visibility, for the log
	requests := 0

	for _, private := range []bool{true, false} {
		repo := e.repos[private]
		for _, r := range secRoutes {
			states := []string{""}
			if r.advisory() {
				states = secStates
			}
			for _, state := range states {
				a := e.advisories[private][state]
				allowed := r.allow(private, state)
				c := cell{r.method + " " + r.pattern, state}
				if summary[c] == nil {
					summary[c] = map[bool][]string{}
				}
				summary[c][private] = allowed
				for _, role := range secRoles {
					body := r.noop
					want := r.ok
					commentID := int64(0)
					if a != nil {
						commentID = e.commentIDs[a.ID]
					}
					denied := !slices.Contains(allowed, role)
					if denied {
						want = http.StatusNotFound
						if r.attack != nil {
							body = r.attack(state)
						}
					} else if r.throwaway {
						c := &damast.AdvisoryComment{AdvisoryID: a.ID, PosterID: e.users["collaborator"].ID, Content: "throwaway"}
						require.NoError(t, damast.CreateAdvisoryComment(db.DefaultContext, c))
						commentID = c.ID
					}
					p := e.path(r, repo, a, commentID)
					resp := e.do(t, role, r.method, p, r.api(), body)
					requests++
					name := fmt.Sprintf("private=%v %s %s state=%s role=%s", private, r.method, r.pattern, state, role)
					assert.Equal(t, want, resp.Code, name)
					e.assertNoLeak(t, name, p, resp.Body.String(), role)
					if r.throwaway && !denied {
						_, err := damast.GetAdvisoryComment(db.DefaultContext, a.ID, commentID)
						assert.ErrorIs(t, err, util.ErrNotExist, "%s: the comment is not deleted", name)
					}
				}
			}
		}
	}

	// guessed and foreign ids: 404 for everyone, including site admins
	for _, role := range secRoles {
		priv, pub := e.repos[true], e.repos[false]
		foreign := e.advisories[true][damast.AdvisoryDraft]
		for _, p := range []string{
			"/" + pub.FullName() + "/security/advisories/" + foreign.AdvisoryID,
			"/api/v1/repos/" + pub.FullName() + "/security-advisories/" + foreign.AdvisoryID,
			"/" + priv.FullName() + "/security/advisories/DAMAST-2222-2222-2222",
			"/api/v1/repos/" + priv.FullName() + "/security-advisories/DAMAST-2222-2222-2222",
			"/" + priv.FullName() + "/security/advisories/1",
			"/api/v1/repos/" + priv.FullName() + "/security-advisories/1",
			"/" + priv.FullName() + "/security/advisories/" + strings.ToLower(foreign.AdvisoryID),
		} {
			resp := e.do(t, role, "GET", p, strings.HasPrefix(p, "/api/"), nil)
			assert.Equal(t, http.StatusNotFound, resp.Code, "%s %s", role, p)
		}
	}

	assert.Equal(t, before, secFingerprint(t), "a request changed advisory data")
	assert.Empty(t, mails, "no request of the matrix may send mail")

	// the matrix for the report: allowed roles per route and state
	var keys []cell
	for c := range summary {
		keys = append(keys, c)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].route != keys[j].route {
			return keys[i].route < keys[j].route
		}
		return slices.Index(secStates, keys[i].state) < slices.Index(secStates, keys[j].state)
	})
	for _, c := range keys {
		t.Logf("MATRIX | %s | %s | private: %s | public: %s |", c.route, c.state, strings.Join(summary[c][true], ","), strings.Join(summary[c][false], ","))
	}
	t.Logf("MATRIX %d requests", requests)
}

// assertNoLeak checks that a response only mentions advisories the role may see, and private comments only
// to participants. An id that is part of the requested path may be echoed (the 404 page links back to it).
// TestDamastSecurityLists checks that lists are complete.
func (e *secEnv) assertNoLeak(t *testing.T, name, path, body, role string) {
	t.Helper()
	for private, byState := range e.advisories {
		for state, a := range byState {
			if slices.Contains(secCanView(private, state), role) {
				continue
			}
			if strings.Contains(body, a.Title) || (!strings.Contains(path, a.AdvisoryID) && strings.Contains(body, a.AdvisoryID)) {
				t.Errorf("%s: leaks %s advisory %s of private=%v", name, state, a.AdvisoryID, private)
			}
		}
	}
	for _, byState := range e.advisories {
		for _, a := range byState {
			if strings.Contains(body, e.comments[a.ID]) && !slices.Contains(secPrivate, role) {
				t.Errorf("%s: leaks the private discussion of %s", name, a.AdvisoryID)
			}
		}
	}
}

func TestDamastSecurityLists(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&mailer.SendAsync, func(...*mailer.Message) {})()
	e := prepareSecurityEnv(t)
	for _, private := range []bool{true, false} {
		repo := e.repos[private]
		for _, role := range secReaders(private) {
			web := e.do(t, role, "GET", "/"+repo.FullName()+"/security", false, nil).Body.String()
			api := e.do(t, role, "GET", "/api/v1/repos/"+repo.FullName()+"/security-advisories", true, nil).Body.String()
			osv := e.do(t, role, "GET", "/api/v1/repos/"+repo.FullName()+"/security-advisories/osv", true, nil).Body.String()
			for state, a := range e.advisories[private] {
				canView := slices.Contains(secCanView(private, state), role)
				assert.Equal(t, canView, strings.Contains(web, a.AdvisoryID), "web %v %s %s", private, role, state)
				assert.Equal(t, canView, strings.Contains(api, a.AdvisoryID), "api %v %s %s", private, role, state)
				assert.Equal(t, secPublic(state), strings.Contains(osv, a.AdvisoryID), "osv %v %s %s", private, role, state)
			}
		}
	}
}

// TestDamastSecurityRoutesCovered fails when a Security route is registered without a row in secRoutes.
func TestDamastSecurityRoutesCovered(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	want := map[string]bool{}
	for _, r := range secRoutes {
		want[r.method+" "+r.pattern] = true
	}
	got := map[string]bool{}
	require.NoError(t, chi.Walk(testWebRoutes.R, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(strings.ReplaceAll(route, "/*/", "/"), "/")
		if method != "HEAD" && strings.Contains(route, "{reponame}/security") {
			got[method+" "+route] = true
		}
		return nil
	}))
	for k := range got {
		assert.True(t, want[k], "route %s has no row in secRoutes", k)
	}
	for k := range want {
		assert.True(t, got[k], "secRoutes row %s is not a registered route", k)
	}
}
