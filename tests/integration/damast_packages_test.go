// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"bytes"
	"fmt"
	"net/http"
	"testing"

	actions_model "forgejo.org/models/actions"
	"forgejo.org/models/damast"
	"forgejo.org/models/db"
	packages_model "forgejo.org/models/packages"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	"forgejo.org/models/unittest"
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
