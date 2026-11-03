// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"testing"

	actions_model "forgejo.org/models/actions"
	"forgejo.org/models/damast"
	"forgejo.org/models/db"
	packages_model "forgejo.org/models/packages"
	"forgejo.org/models/perm"
	access_model "forgejo.org/models/perm/access"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	api "forgejo.org/modules/structs"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// user4 is a write collaborator on user5/repo4 (fixtures) but has no owner-level package access on user5.
func TestDamastRepoBoundPackages(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := db.DefaultContext
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: 4, Type: unit.TypePackages}))

	put := func(user, name, version string, status int) {
		t.Helper()
		url := fmt.Sprintf("/api/packages/user5/generic/%s/%s/file.bin", name, version)
		MakeRequest(t, NewRequestWithBody(t, "PUT", url, bytes.NewReader([]byte("x"))).AddBasicAuth(user), status)
	}
	pkg := func(name string) *packages_model.Package {
		t.Helper()
		p, err := packages_model.GetPackageByName(ctx, 5, packages_model.TypeGeneric, name)
		if err != nil {
			return nil
		}
		return p
	}

	t.Run("new package named like a writable repo is created and linked", func(t *testing.T) {
		put("user4", "repo4", "1.0", http.StatusCreated)
		p := pkg("repo4")
		require.NotNil(t, p)
		assert.EqualValues(t, 4, p.RepoID)
		inherit, err := damast.InheritsRepoAccess(ctx, p.ID)
		require.NoError(t, err)
		assert.True(t, inherit)
	})

	t.Run("new package without a writable repo is rejected and rolled back", func(t *testing.T) {
		put("user4", "unrelated", "1.0", http.StatusForbidden)
		assert.Nil(t, pkg("unrelated"))
	})

	t.Run("further versions of the linked package are allowed", func(t *testing.T) {
		put("user4", "repo4", "1.1", http.StatusCreated)
	})

	t.Run("delete keeps owner-level rules", func(t *testing.T) {
		MakeRequest(t, NewRequest(t, "DELETE", "/api/packages/user5/generic/repo4/1.0").AddBasicAuth("user4"), http.StatusUnauthorized)
	})

	t.Run("unlinked package of the owner stays owner-only", func(t *testing.T) {
		put("user5", "legacy", "1.0", http.StatusCreated)
		assert.Zero(t, pkg("legacy").RepoID)
		put("user4", "legacy", "1.1", http.StatusForbidden)
	})

	t.Run("maven: artifact named like the repo, pom update of foreign version rejected", func(t *testing.T) {
		mvn := func(user, artifact, file, body string, status int) {
			t.Helper()
			url := fmt.Sprintf("/api/packages/user5/maven/com/example/%s/1.0/%s", artifact, file)
			MakeRequest(t, NewRequestWithBody(t, "PUT", url, bytes.NewReader([]byte(body))).AddBasicAuth(user), status)
		}
		pom := func(artifact string) string {
			return `<?xml version="1.0"?><project><groupId>com.example</groupId><artifactId>` + artifact + `</artifactId><version>1.0</version></project>`
		}

		mvn("user4", "repo4", "repo4-1.0.jar", "jar", http.StatusCreated)
		p, err := packages_model.GetPackageByName(ctx, 5, packages_model.TypeMaven, "com.example:repo4")
		require.NoError(t, err)
		assert.EqualValues(t, 4, p.RepoID)
		mvn("user4", "repo4", "repo4-1.0.pom", pom("repo4"), http.StatusCreated)

		// owner publishes an unlinked artifact; the collaborator must not rewrite its metadata
		mvn("user5", "internal", "internal-1.0.jar", "jar", http.StatusCreated)
		mvn("user4", "internal", "internal-1.0.pom", pom("internal"), http.StatusForbidden)
	})

	t.Run("users without repo write get nothing", func(t *testing.T) {
		put("user2", "repo4", "2.0", http.StatusUnauthorized)
	})

	t.Run("inheritance switched off in package settings", func(t *testing.T) {
		session := loginUser(t, "user5")
		link := "/user5/-/packages/generic/repo4/1.1/settings"
		doc := NewHTMLParser(t, session.MakeRequest(t, NewRequest(t, "GET", link), http.StatusOK).Body)
		assert.True(t, doc.Find("#damast-inherit").Is("[checked]"))
		session.MakeRequest(t, NewRequestWithValues(t, "POST", link, map[string]string{
			"action": "damast_inherit",
		}), http.StatusSeeOther)

		inherit, err := damast.InheritsRepoAccess(ctx, pkg("repo4").ID)
		require.NoError(t, err)
		assert.False(t, inherit)
		put("user4", "repo4", "3.0", http.StatusForbidden)
	})
}

// Actions task 47 (fixtures) runs in user5/repo4.
func TestDamastActionsTokenPackages(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := db.DefaultContext
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: 4, Type: unit.TypePackages}))

	task := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: 47})
	setTask := func(repoID, ownerID int64, fork bool) string {
		t.Helper()
		task.RepoID, task.OwnerID, task.IsForkPullRequest = repoID, ownerID, fork
		task.GenerateToken()
		require.NoError(t, actions_model.UpdateTask(ctx, task, "repo_id", "owner_id", "is_fork_pull_request", "token_hash", "token_salt", "token_last_eight"))
		return task.Token
	}
	put := func(token, name, version string, status int) {
		t.Helper()
		url := fmt.Sprintf("/api/packages/user5/generic/%s/%s/file.bin", name, version)
		MakeRequest(t, NewRequestWithBody(t, "PUT", url, bytes.NewReader([]byte("x"))).AddTokenAuth(token), status)
	}

	t.Run("token of a repo publishes to its owner and links the package to the run's repo", func(t *testing.T) {
		put(setTask(4, 5, false), "ci-artifact", "1.0", http.StatusCreated)
		p, err := packages_model.GetPackageByName(ctx, 5, packages_model.TypeGeneric, "ci-artifact")
		require.NoError(t, err)
		assert.EqualValues(t, 4, p.RepoID)
		put(task.Token, "ci-artifact", "1.1", http.StatusCreated)
	})

	t.Run("token cannot touch packages not linked to its repo", func(t *testing.T) {
		MakeRequest(t, NewRequestWithBody(t, "PUT", "/api/packages/user5/generic/manual/1.0/file.bin", bytes.NewReader([]byte("x"))).AddBasicAuth("user5"), http.StatusCreated)
		put(setTask(4, 5, false), "manual", "1.1", http.StatusForbidden)
	})

	t.Run("fork pull request runs get no write", func(t *testing.T) {
		put(setTask(4, 5, true), "ci-artifact", "2.0", http.StatusUnauthorized)
	})

	t.Run("token of another owner's repo gets no write", func(t *testing.T) {
		put(setTask(1, 2, false), "ci-artifact", "3.0", http.StatusUnauthorized)
	})
}

// Phase 2: with a private owner, repository access is the only way to read a package.
func TestDamastRepoBoundPackageRead(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := db.DefaultContext
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: 4, Type: unit.TypePackages}))
	_, err := db.GetEngine(ctx).ID(5).Cols("visibility").Update(&user_model.User{Visibility: api.VisibleTypePrivate})
	require.NoError(t, err)
	// user2: read-only collaborator on user5/repo4
	require.NoError(t, db.Insert(ctx, &repo_model.Collaboration{RepoID: 4, UserID: 2, Mode: perm.AccessModeRead}))
	require.NoError(t, db.Insert(ctx, &access_model.Access{RepoID: 4, UserID: 2, Mode: perm.AccessModeRead}))

	upload := func(user, name string) {
		t.Helper()
		url := fmt.Sprintf("/api/packages/user5/generic/%s/1.0/file.bin", name)
		MakeRequest(t, NewRequestWithBody(t, "PUT", url, bytes.NewReader([]byte("x"))).AddBasicAuth(user), http.StatusCreated)
	}
	upload("user4", "repo4")  // linked to repo4, inherits access
	upload("user5", "secret") // not linked

	type auth func(*RequestWrapper) *RequestWrapper
	get := func(as auth, name string, status int) {
		t.Helper()
		MakeRequest(t, as(NewRequest(t, "GET", fmt.Sprintf("/api/packages/user5/generic/%s/1.0/file.bin", name))), status)
	}
	user := func(name string) auth { return func(r *RequestWrapper) *RequestWrapper { return r.AddBasicAuth(name) } }

	t.Run("read collaborator reads the linked package only", func(t *testing.T) {
		get(user("user2"), "repo4", http.StatusOK)
		get(user("user2"), "secret", http.StatusNotFound) // same as a missing package
		get(user("user2"), "missing", http.StatusNotFound)
	})

	t.Run("read collaborator cannot upload", func(t *testing.T) {
		MakeRequest(t, NewRequestWithBody(t, "PUT", "/api/packages/user5/generic/repo4/2.0/file.bin", bytes.NewReader([]byte("x"))).AddBasicAuth("user2"), http.StatusUnauthorized)
	})

	t.Run("users without repo access read nothing", func(t *testing.T) {
		get(user("user8"), "repo4", http.StatusUnauthorized)
	})

	task := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: 47})
	setTask := func(fork bool) string {
		task.RepoID, task.OwnerID, task.IsForkPullRequest = 4, 5, fork
		task.GenerateToken()
		require.NoError(t, actions_model.UpdateTask(ctx, task, "repo_id", "owner_id", "is_fork_pull_request", "token_hash", "token_salt", "token_last_eight"))
		return task.Token
	}
	token := func(fork bool) auth {
		tok := setTask(fork)
		return func(r *RequestWrapper) *RequestWrapper { return r.AddTokenAuth(tok) }
	}

	t.Run("Actions token reads its repo's packages, also from fork PRs", func(t *testing.T) {
		get(token(false), "repo4", http.StatusOK)
		get(token(true), "repo4", http.StatusOK)
		get(token(false), "secret", http.StatusNotFound)
	})

	t.Run("container HEAD and GET don't reveal unreadable images", func(t *testing.T) {
		v2token := func(user string) string {
			t.Helper()
			resp := MakeRequest(t, NewRequest(t, "GET", "/v2/token").AddBasicAuth(user), http.StatusOK)
			var tr struct {
				Token string `json:"token"`
			}
			DecodeJSON(t, resp, &tr)
			return "Bearer " + tr.Token
		}
		content := []byte("damast")
		digest := fmt.Sprintf("sha256:%x", sha256.Sum256(content))
		MakeRequest(t, NewRequestWithBody(t, "POST", "/v2/user5/secret/blobs/uploads?digest="+digest, bytes.NewReader(content)).
			AddTokenAuth(v2token("user5")), http.StatusCreated)

		reader := v2token("user2")
		for _, method := range []string{"HEAD", "GET"} {
			MakeRequest(t, NewRequest(t, method, "/v2/user5/secret/blobs/"+digest).AddTokenAuth(reader), http.StatusNotFound)
			MakeRequest(t, NewRequest(t, method, "/v2/user5/secret/manifests/latest").AddTokenAuth(reader), http.StatusNotFound)
		}
		// the owner still sees it
		MakeRequest(t, NewRequest(t, "HEAD", "/v2/user5/secret/blobs/"+digest).AddTokenAuth(v2token("user5")), http.StatusOK)
	})

	t.Run("listing endpoints stay owner-level", func(t *testing.T) {
		MakeRequest(t, NewRequest(t, "GET", "/api/packages/user5/npm/-/v1/search?text=repo4").AddBasicAuth("user2"), http.StatusUnauthorized)
	})
}
