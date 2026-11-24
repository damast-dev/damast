// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"fmt"
	"net/http"
	"testing"

	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/damast"
	"forgejo.org/models/db"
	org_model "forgejo.org/models/organization"
	packages_model "forgejo.org/models/packages"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	org_service "forgejo.org/services/org"
	repo_service "forgejo.org/services/repository"
	user_service "forgejo.org/services/user"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Deleted packages, repositories and owners take their Damast rows with them, so a package or account that
// later gets the same ID starts with the defaults.
func TestDamastPackagesCleanup(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := db.DefaultContext

	policyPackage := func(t *testing.T, ownerID int64, name string) *packages_model.Package {
		t.Helper()
		p, err := packages_model.TryInsertPackage(ctx, &packages_model.Package{OwnerID: ownerID, Type: packages_model.TypeGeneric, Name: name, LowerName: name})
		require.NoError(t, err)
		require.NoError(t, damast.SetInheritRepoAccess(ctx, p.ID, true))
		return p
	}
	gone := func(t *testing.T, id int64, bean any) {
		t.Helper()
		has, err := db.GetEngine(ctx).ID(id).Exist(bean)
		require.NoError(t, err)
		assert.False(t, has, "%T %d", bean, id)
	}
	assertDefaults := func(t *testing.T, ownerID, packageID int64) {
		t.Helper()
		gone(t, ownerID, &damast.OwnerPackagePolicy{})
		gone(t, packageID, &damast.PackagePolicy{})
	}

	t.Run("package", func(t *testing.T) {
		p := policyPackage(t, 2, "gone")
		require.NoError(t, packages_model.DeletePackageByID(ctx, p.ID))
		gone(t, p.ID, &damast.PackagePolicy{})
	})

	t.Run("repository", func(t *testing.T) {
		require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: 4, Type: unit.TypePackages}))
		damastPutGeneric(t, "user5", "repo4", "1.0", http.StatusCreated)
		p, err := packages_model.GetPackageByName(ctx, 5, packages_model.TypeGeneric, "repo4")
		require.NoError(t, err)
		require.EqualValues(t, 4, p.RepoID)
		require.NoError(t, repo_service.DeleteRepositoryDirectly(ctx, 4, repo_service.DeleteRepositoryOpts{}))
		gone(t, p.ID, &damast.PackagePolicy{})
	})

	t.Run("user, ID reused", func(t *testing.T) {
		u := &user_model.User{Name: "pkgowner", Email: "pkgowner@example.com", Passwd: "password1!"}
		require.NoError(t, user_model.CreateUser(ctx, u))
		require.NoError(t, damast.SetRepoWritersMayCreatePackages(ctx, u.ID, true))
		p := policyPackage(t, u.ID, "left-behind")

		require.NoError(t, user_service.DeleteUser(ctx, u, false))
		assertDefaults(t, u.ID, p.ID)

		require.NoError(t, db.Insert(ctx, &user_model.User{ID: u.ID, Name: "newcomer", LowerName: "newcomer", Email: "newcomer@example.com"}))
		allow, err := damast.RepoWritersMayCreatePackages(ctx, u.ID)
		require.NoError(t, err)
		assert.False(t, allow, "the new account starts with owners only")
	})

	t.Run("organization", func(t *testing.T) {
		owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		org := &org_model.Organization{Name: "pkgorg"}
		require.NoError(t, org_model.CreateOrganization(ctx, org, owner))
		require.NoError(t, damast.SetRepoWritersMayCreatePackages(ctx, org.ID, true))
		p := policyPackage(t, org.ID, "left-behind")

		require.NoError(t, org_service.DeleteOrganization(ctx, org, false))
		assertDefaults(t, org.ID, p.ID)
	})
}

// GET/PATCH /api/v1/damast/packages/{owner}/{type}/{name}/settings: the per-package switch for API clients.
func TestDamastPackageSettingsAPI(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, db.Insert(db.DefaultContext, &repo_model.RepoUnit{RepoID: 4, Type: unit.TypePackages}))
	damastPutGeneric(t, "user5", "repo4", "1.0", http.StatusCreated)

	url := "/api/v1/damast/packages/user5/generic/repo4/settings"
	owner := getUserToken(t, "user5", auth_model.AccessTokenScopeWritePackage)
	inherit := func(token string) bool {
		t.Helper()
		var s struct {
			InheritRepoAccess bool `json:"inherit_repo_access"`
		}
		DecodeJSON(t, MakeRequest(t, NewRequest(t, "GET", url).AddTokenAuth(token), http.StatusOK), &s)
		return s.InheritRepoAccess
	}
	assert.True(t, inherit(owner))

	writer := getUserToken(t, "user4", auth_model.AccessTokenScopeWritePackage)
	assert.True(t, inherit(writer), "readers of the owner see the switch")
	MakeRequest(t, NewRequestWithJSON(t, "PATCH", url, map[string]bool{"inherit_repo_access": false}).AddTokenAuth(writer), http.StatusForbidden)

	MakeRequest(t, NewRequestWithJSON(t, "PATCH", url, map[string]bool{"inherit_repo_access": false}).AddTokenAuth(owner), http.StatusOK)
	assert.False(t, inherit(owner))
	damastPutGeneric(t, "user4", "repo4", "1.1", http.StatusForbidden) // repo write no longer counts

	MakeRequest(t, NewRequest(t, "GET", "/api/v1/damast/packages/user5/generic/missing/settings").AddTokenAuth(owner), http.StatusNotFound)
	MakeRequest(t, NewRequest(t, "GET", "/api/v1/damast/packages/user5/nope/repo4/settings").AddTokenAuth(owner), http.StatusNotFound)
}

// Unlinking (or moving the link to another repository) turns repository access off: the next repository's
// readers and writers only count once an owner switches it on again.
func TestDamastPackagesUnlinkResetsInherit(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := db.DefaultContext
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: 4, Type: unit.TypePackages}))
	damastPutGeneric(t, "user5", "repo4", "1.0", http.StatusCreated)
	p, err := packages_model.GetPackageByName(ctx, 5, packages_model.TypeGeneric, "repo4")
	require.NoError(t, err)
	inherits := func() bool {
		t.Helper()
		on, err := damast.InheritsRepoAccess(ctx, p.ID)
		require.NoError(t, err)
		return on
	}
	require.True(t, inherits())

	owner := getUserToken(t, "user5", auth_model.AccessTokenScopeWritePackage, auth_model.AccessTokenScopeWriteRepository)
	MakeRequest(t, NewRequest(t, "POST", "/api/v1/packages/user5/generic/repo4/-/unlink").AddTokenAuth(owner), http.StatusNoContent)
	assert.False(t, inherits(), "unlink turns it off")
	MakeRequest(t, NewRequest(t, "POST", "/api/v1/packages/user5/generic/repo4/-/link/repo4").AddTokenAuth(owner), http.StatusCreated)
	assert.False(t, inherits(), "relinking doesn't turn it on")
	damastPutGeneric(t, "user4", "repo4", "1.1", http.StatusForbidden)

	require.NoError(t, damast.SetInheritRepoAccess(ctx, p.ID, true))
	other := damastRepo(t, unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5}), "other", true)
	s := loginUser(t, "user5")
	s.MakeRequest(t, NewRequestWithValues(t, "POST", "/user5/-/packages/generic/repo4/1.0/settings", map[string]string{
		"action": "link", "repo_id": fmt.Sprint(other.ID),
	}), http.StatusSeeOther)
	p, err = packages_model.GetPackageByID(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, other.ID, p.RepoID)
	assert.False(t, inherits(), "moving the link to another repository turns it off")
}
