// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"net/http"
	"net/http/httptest"
	"testing"

	auth_model "forgejo.org/models/auth"
	api "forgejo.org/modules/structs"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Repository readers see the packages they may read in the package API and on the owner's package pages,
// like on GitHub. Everything else looks missing, including counts and pagination.
func TestDamastPackagesListInherited(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	damastPrivateOwner(t)                                            // user5 private, user2 reads user5/repo4
	damastPutGeneric(t, "user5", "repo4", "1.0", http.StatusCreated) // linked to repo4 by name
	damastPutGeneric(t, "user5", "repo4", "1.1", http.StatusCreated)
	damastPutGeneric(t, "user5", "secret", "1.0", http.StatusCreated) // not linked

	reader := getUserToken(t, "user2", auth_model.AccessTokenScopeReadPackage)
	get := func(token, url string, status int) *httptest.ResponseRecorder {
		t.Helper()
		return MakeRequest(t, NewRequest(t, "GET", url).AddTokenAuth(token), status)
	}
	sameAsMissing := func(t *testing.T, token, hidden, missing string) {
		t.Helper()
		h, m := get(token, hidden, http.StatusNotFound), get(token, missing, http.StatusNotFound)
		assert.Equal(t, m.Header().Get("Content-Type"), h.Header().Get("Content-Type"))
		assert.Equal(t, m.Body.String(), h.Body.String())
	}

	t.Run("API list", func(t *testing.T) {
		var pkgs []*api.Package
		resp := get(reader, "/api/v1/packages/user5", http.StatusOK)
		DecodeJSON(t, resp, &pkgs)
		require.Len(t, pkgs, 2)
		for _, p := range pkgs {
			assert.Equal(t, "repo4", p.Name)
		}
		assert.Equal(t, "2", resp.Header().Get("X-Total-Count"))

		resp = get(reader, "/api/v1/packages/user5?limit=1&page=2", http.StatusOK)
		DecodeJSON(t, resp, &pkgs)
		require.Len(t, pkgs, 1)
		assert.Equal(t, "2", resp.Header().Get("X-Total-Count"))

		DecodeJSON(t, get(reader, "/api/v1/packages/user5?q=secret", http.StatusOK), &pkgs)
		assert.Empty(t, pkgs)
	})

	t.Run("API info and files", func(t *testing.T) {
		var p api.Package
		DecodeJSON(t, get(reader, "/api/v1/packages/user5/generic/repo4/1.0", http.StatusOK), &p)
		assert.Equal(t, "repo4", p.Name)
		get(reader, "/api/v1/packages/user5/generic/repo4/1.0/files", http.StatusOK)
		sameAsMissing(t, reader, "/api/v1/packages/user5/generic/secret/1.0", "/api/v1/packages/user5/generic/missing/1.0")
		sameAsMissing(t, reader, "/api/v1/packages/user5/generic/secret/1.0/files", "/api/v1/packages/user5/generic/missing/1.0/files")
	})

	t.Run("API Damast settings", func(t *testing.T) {
		get(reader, "/api/v1/damast/packages/user5/generic/repo4/settings", http.StatusOK)
		sameAsMissing(t, reader, "/api/v1/damast/packages/user5/generic/secret/settings", "/api/v1/damast/packages/user5/generic/missing/settings")
		get(reader, "/api/v1/damast/packages/user5/settings", http.StatusForbidden) // owners only
	})

	t.Run("API writes stay owner-level", func(t *testing.T) {
		writer := getUserToken(t, "user2", auth_model.AccessTokenScopeWritePackage)
		MakeRequest(t, NewRequest(t, "DELETE", "/api/v1/packages/user5/generic/repo4/1.0").AddTokenAuth(writer), http.StatusForbidden)
		MakeRequest(t, NewRequest(t, "POST", "/api/v1/packages/user5/generic/repo4/-/unlink").AddTokenAuth(writer), http.StatusForbidden)
	})

	t.Run("users without repository access", func(t *testing.T) {
		get(getUserToken(t, "user8", auth_model.AccessTokenScopeReadPackage), "/api/v1/packages/user5", http.StatusForbidden)
	})

	// The owner's package pages show the private owner's profile or organization header, which Forgejo
	// hides from non-members; they keep Forgejo's owner-level rule. Hidden and missing look the same.
	t.Run("web pages keep owner visibility", func(t *testing.T) {
		s := loginUser(t, "user2")
		s.MakeRequest(t, NewRequest(t, "GET", "/user5/-/packages"), http.StatusNotFound)
		h := s.MakeRequest(t, NewRequest(t, "GET", "/user5/-/packages/generic/secret/1.0"), http.StatusNotFound).Body.String()
		m := s.MakeRequest(t, NewRequest(t, "GET", "/user5/-/packages/generic/missing/1.0"), http.StatusNotFound).Body.String()
		assert.Equal(t, m, h)
	})
}
