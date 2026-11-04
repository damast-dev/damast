// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	actions_model "forgejo.org/models/actions"
	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/db"
	packages_model "forgejo.org/models/packages"
	"forgejo.org/models/perm"
	access_model "forgejo.org/models/perm/access"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	repo_service "forgejo.org/services/repository"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// damastPrivateOwner makes user5 private, enables packages on user5/repo4 and adds user2 as read collaborator.
// user4 is a write collaborator on repo4 (fixtures).
func damastPrivateOwner(t *testing.T) {
	t.Helper()
	ctx := db.DefaultContext
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: 4, Type: unit.TypePackages}))
	_, err := db.GetEngine(ctx).ID(5).Cols("visibility").Update(&user_model.User{Visibility: api.VisibleTypePrivate})
	require.NoError(t, err)
	require.NoError(t, db.Insert(ctx, &repo_model.Collaboration{RepoID: 4, UserID: 2, Mode: perm.AccessModeRead}))
	require.NoError(t, db.Insert(ctx, &access_model.Access{RepoID: 4, UserID: 2, Mode: perm.AccessModeRead}))
}

// damastRepo inserts a repository row with the packages unit, enough for permission checks.
func damastRepo(t *testing.T, owner *user_model.User, name string, private bool) *repo_model.Repository {
	t.Helper()
	repo := &repo_model.Repository{OwnerID: owner.ID, OwnerName: owner.Name, Name: name, LowerName: strings.ToLower(name), IsPrivate: private}
	require.NoError(t, db.Insert(db.DefaultContext, repo))
	require.NoError(t, db.Insert(db.DefaultContext, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypePackages}))
	return repo
}

func damastCollaborator(t *testing.T, repoID, userID int64, mode perm.AccessMode) {
	t.Helper()
	require.NoError(t, db.Insert(db.DefaultContext, &repo_model.Collaboration{RepoID: repoID, UserID: userID, Mode: mode}))
	require.NoError(t, db.Insert(db.DefaultContext, &access_model.Access{RepoID: repoID, UserID: userID, Mode: mode}))
}

func damastPutGeneric(t *testing.T, user, name, version string, status int) {
	t.Helper()
	u := fmt.Sprintf("/api/packages/user5/generic/%s/%s/file.bin", name, version)
	MakeRequest(t, NewRequestWithBody(t, "PUT", u, bytes.NewReader([]byte("x"))).AddBasicAuth(user), status)
}

// Read paths that answer from the database without the package descriptor or file stream.
func TestDamastPackagesReadOracles(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	damastPrivateOwner(t)

	damastPutGeneric(t, "user4", "repo4", "1.0", http.StatusCreated) // linked to repo4
	damastPutGeneric(t, "user5", "secret", "1.0", http.StatusCreated)

	mvn := func(user, method, artifact, file string, status int) *http.Response {
		t.Helper()
		u := fmt.Sprintf("/api/packages/user5/maven/com/example/%s/1.0/%s", artifact, file)
		var body io.Reader
		if method == "PUT" {
			body = bytes.NewReader([]byte("jar"))
		}
		return MakeRequest(t, NewRequestWithBody(t, method, u, body).AddBasicAuth(user), status).Result()
	}
	mvn("user4", "PUT", "repo4", "repo4-1.0.jar", http.StatusCreated)
	mvn("user5", "PUT", "secret", "secret-1.0.jar", http.StatusCreated)

	t.Run("generic HEAD", func(t *testing.T) {
		MakeRequest(t, NewRequest(t, "HEAD", "/api/packages/user5/generic/repo4/1.0/file.bin").AddBasicAuth("user2"), http.StatusOK)
		MakeRequest(t, NewRequest(t, "HEAD", "/api/packages/user5/generic/secret/1.0/file.bin").AddBasicAuth("user2"), http.StatusNotFound)
	})

	t.Run("maven HEAD and checksums", func(t *testing.T) {
		mvn("user2", "HEAD", "repo4", "repo4-1.0.jar", http.StatusOK)
		mvn("user2", "GET", "repo4", "repo4-1.0.jar.sha256", http.StatusOK)
		mvn("user2", "HEAD", "secret", "secret-1.0.jar", http.StatusNotFound)
		for _, ext := range []string{"md5", "sha1", "sha256", "sha512"} {
			mvn("user2", "GET", "secret", "secret-1.0.jar."+ext, http.StatusNotFound)
		}
	})

	t.Run("a hidden package answers exactly like a missing one", func(t *testing.T) {
		get := func(name string) string {
			t.Helper()
			resp := MakeRequest(t, NewRequest(t, "GET", "/api/packages/user5/generic/"+name+"/1.0/file.bin").AddBasicAuth("user2"), http.StatusNotFound)
			return resp.Body.String()
		}
		assert.Equal(t, get("missing"), get("secret"))
	})
}

// A link to a repository outside the package owner must not grant anything: npm relinks packages to any
// repository named in package.json the publisher can write to, and transfers keep the link.
func TestDamastPackagesForeignRepoLink(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	damastPrivateOwner(t)
	ctx := db.DefaultContext
	user4 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	user5 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	other := damastRepo(t, user5, "other", true)
	damastCollaborator(t, other.ID, 4, perm.AccessModeWrite) // user4 keeps write somewhere in user5
	evil := damastRepo(t, user4, "evil", false)

	t.Run("npm repository URL of another owner", func(t *testing.T) {
		name := "@user5/repo4"
		upload := func(version string, status int) {
			t.Helper()
			data := "H4sIAAAAAAAA/ytITM5OTE/VL4DQelnF+XkMVAYGBgZmJiYK2MRBwNDcSIHB2NTMwNDQzMwAqA7IMDUxA9LUdgg2UFpcklgEdAql5kD8ogCnhwio5lJQUMpLzE1VslJQcihOzi9I1S9JLS7RhSYIJR2QgrLUouLM/DyQGkM9Az1D3YIiqExKanFyUWZBCVQ2BKhVwQVJDKwosbQkI78IJO/tZ+LsbRykxFXLNdA+HwWjYBSMgpENACgAbtAACAAA"
			body := `{"_id":"` + name + `","name":"` + name + `","dist-tags":{"latest":"` + version + `"},
				"versions":{"` + version + `":{"name":"` + name + `","version":"` + version + `",
				"dist":{"integrity":"sha512-yA4FJsVhetynGfOC1jFf79BuS+jrHbm0fhh+aHzCQkOaOBXKf9oBnC4a6DnLLnEsHQDRLYd00cwj8sCXpC+wIg==","shasum":"aaa7eaf852a948b0aa05afeda35b1badca155d90"},
				"repository":{"type":"git","url":"` + setting.AppURL + `user4/evil.git"}}},
				"_attachments":{"` + name + `-` + version + `.tgz":{"data":"` + data + `"}}}`
			MakeRequest(t, NewRequestWithBody(t, "PUT", "/api/packages/user5/npm/"+url.QueryEscape(name), strings.NewReader(body)).AddBasicAuth("user4"), status)
		}
		upload("1.0.0", http.StatusCreated)
		p, err := packages_model.GetPackageByName(ctx, 5, packages_model.TypeNpm, name)
		require.NoError(t, err)
		require.Equal(t, evil.ID, p.RepoID, "upstream npm relinks to the repository from package.json")

		// user5 removes user4 from repo4; the package must not stay writable through user4's own repo
		_, err = db.GetEngine(ctx).Where("repo_id = 4 AND user_id = 4").Delete(&repo_model.Collaboration{})
		require.NoError(t, err)
		_, err = db.GetEngine(ctx).Where("repo_id = 4 AND user_id = 4").Delete(&access_model.Access{})
		require.NoError(t, err)
		upload("1.0.1", http.StatusForbidden)
		MakeRequest(t, NewRequest(t, "GET", "/api/packages/user5/npm/"+url.QueryEscape(name)).AddBasicAuth("user4"), http.StatusNotFound)
	})

	t.Run("repository transferred to another owner", func(t *testing.T) {
		damastCollaborator(t, 4, 4, perm.AccessModeWrite)
		damastPutGeneric(t, "user4", "repo4", "1.0", http.StatusCreated)
		_, err := db.GetEngine(ctx).Where("repo_id = 4 AND user_id = 4").Delete(&repo_model.Collaboration{})
		require.NoError(t, err)

		repo4 := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})
		require.NoError(t, repo_service.TransferOwnership(ctx, user5, user4, repo4, nil))
		p, err := packages_model.GetPackageByName(ctx, 5, packages_model.TypeGeneric, "repo4")
		require.NoError(t, err)
		require.EqualValues(t, 4, p.RepoID, "Forgejo keeps the link on transfer")

		// user4 now owns repo4, but the package still belongs to user5
		damastPutGeneric(t, "user4", "repo4", "1.1", http.StatusForbidden)
		MakeRequest(t, NewRequest(t, "GET", "/api/packages/user5/generic/repo4/1.0/file.bin").AddBasicAuth("user4"), http.StatusNotFound)
	})
}

// Forgejo gives prohibited, inactive and 2FA-lacking users no package access; provisional access must not either.
func TestDamastPackagesBlockedUser(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, db.Insert(db.DefaultContext, &repo_model.RepoUnit{RepoID: 4, Type: unit.TypePackages}))
	token := getTokenForLoggedInUser(t, loginUser(t, "user4"), auth_model.AccessTokenScopeWritePackage)
	put := func(version string, status int) {
		t.Helper()
		u := fmt.Sprintf("/api/packages/user5/generic/repo4/%s/file.bin", version)
		MakeRequest(t, NewRequestWithBody(t, "PUT", u, bytes.NewReader([]byte("x"))).AddTokenAuth(token), status)
	}
	put("1.0", http.StatusCreated)

	_, err := db.GetEngine(db.DefaultContext).ID(4).Cols("prohibit_login").Update(&user_model.User{ProhibitLogin: true})
	require.NoError(t, err)
	put("1.1", http.StatusUnauthorized)
}

// OPEN FINDING, documents current behaviour: every non-fork Actions run (and every repo writer) can claim any
// unused package name, also in registries with one signed index per owner. Flip once decided.
func TestDamastPackagesOpenFindingNameClaim(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := db.DefaultContext
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: 4, Type: unit.TypePackages}))
	task := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: 47})
	task.RepoID, task.OwnerID, task.IsForkPullRequest = 4, 5, false
	task.GenerateToken()
	require.NoError(t, actions_model.UpdateTask(ctx, task, "repo_id", "owner_id", "is_fork_pull_request", "token_hash", "token_salt", "token_last_eight"))

	// "curl 99" in user5's signed apt repository, published by a workflow of repo4
	MakeRequest(t, NewRequestWithBody(t, "PUT", "/api/packages/user5/debian/pool/stable/main/upload",
		createDebianArchive("curl", "99.0", "amd64", "not curl")).AddTokenAuth(task.Token), http.StatusCreated)
	p, err := packages_model.GetPackageByName(ctx, 5, packages_model.TypeDebian, "curl")
	require.NoError(t, err)
	assert.EqualValues(t, 4, p.RepoID)
	resp := MakeRequest(t, NewRequest(t, "GET", "/api/packages/user5/debian/dists/stable/main/binary-amd64/Packages"), http.StatusOK)
	assert.Contains(t, resp.Body.String(), "Package: curl")
}
