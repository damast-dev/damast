// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	gocontext "context"
	"crypto/sha256"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	actions_model "forgejo.org/models/actions"
	auth_model "forgejo.org/models/auth"
	"forgejo.org/models/damast"
	"forgejo.org/models/db"
	packages_model "forgejo.org/models/packages"
	"forgejo.org/models/perm"
	access_model "forgejo.org/models/perm/access"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/test"
	damast_packages "forgejo.org/services/damast/packages"
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

// damastRepoWritersCreate lets repository writers and Actions runs create new packages of the owner.
func damastRepoWritersCreate(t *testing.T, ownerID int64) {
	t.Helper()
	require.NoError(t, damast.SetRepoWritersMayCreatePackages(db.DefaultContext, ownerID, true))
}

func damastPutGeneric(t *testing.T, user, name, version string, status int) {
	t.Helper()
	u := fmt.Sprintf("/api/packages/user5/generic/%s/%s/file.bin", name, version)
	MakeRequest(t, NewRequestWithBody(t, "PUT", u, bytes.NewReader([]byte("x"))).AddBasicAuth(user), status)
}

func damastPutNpm(t *testing.T, user, name, version, repoURL string, status int) {
	t.Helper()
	data := "H4sIAAAAAAAA/ytITM5OTE/VL4DQelnF+XkMVAYGBgZmJiYK2MRBwNDcSIHB2NTMwNDQzMwAqA7IMDUxA9LUdgg2UFpcklgEdAql5kD8ogCnhwio5lJQUMpLzE1VslJQcihOzi9I1S9JLS7RhSYIJR2QgrLUouLM/DyQGkM9Az1D3YIiqExKanFyUWZBCVQ2BKhVwQVJDKwosbQkI78IJO/tZ+LsbRykxFXLNdA+HwWjYBSMgpENACgAbtAACAAA"
	body := `{"_id":"` + name + `","name":"` + name + `","dist-tags":{"latest":"` + version + `"},
		"versions":{"` + version + `":{"name":"` + name + `","version":"` + version + `",
		"dist":{"integrity":"sha512-yA4FJsVhetynGfOC1jFf79BuS+jrHbm0fhh+aHzCQkOaOBXKf9oBnC4a6DnLLnEsHQDRLYd00cwj8sCXpC+wIg==","shasum":"aaa7eaf852a948b0aa05afeda35b1badca155d90"},
		"repository":{"type":"git","url":"` + repoURL + `"}}},
		"_attachments":{"` + name + `-` + version + `.tgz":{"data":"` + data + `"}}}`
	MakeRequest(t, NewRequestWithBody(t, "PUT", "/api/packages/user5/npm/"+url.QueryEscape(name), strings.NewReader(body)).AddBasicAuth(user), status)
}

// Read paths that answer from the database without the package descriptor or file stream.
func TestDamastPackagesReadOracles(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	damastRepoWritersCreate(t, 5)
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
	damastRepoWritersCreate(t, 5)
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
			damastPutNpm(t, "user4", name, version, setting.AppURL+"user4/evil.git", status)
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
	damastRepoWritersCreate(t, 5)
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

// Hidden packages answer exactly like missing ones (status, type and body), also on paths that look
// packages up in lists: npm/PyPI/Maven metadata and the container registry.
func TestDamastPackagesHiddenLooksMissing(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	damastRepoWritersCreate(t, 5)
	damastPrivateOwner(t)

	// user5 publishes unlinked packages named "secret"; user2 only reads repo4
	damastPutNpm(t, "user5", "@user5/secret", "1.0.0", setting.AppURL+"user5/nothing", http.StatusCreated)
	MakeRequest(t, NewRequestWithBody(t, "PUT", "/api/packages/user5/maven/com/example/secret/1.0/secret-1.0.jar",
		bytes.NewReader([]byte("jar"))).AddBasicAuth("user5"), http.StatusCreated)
	{
		content := "wheel"
		body := &bytes.Buffer{}
		w := multipart.NewWriter(body)
		part, _ := w.CreateFormFile("content", "secret-1.0-py3-none-any.whl")
		_, _ = part.Write([]byte(content))
		_ = w.WriteField("name", "secret")
		_ = w.WriteField("version", "1.0")
		_ = w.WriteField("sha256_digest", fmt.Sprintf("%x", sha256.Sum256([]byte(content))))
		require.NoError(t, w.Close())
		MakeRequest(t, NewRequestWithBody(t, "POST", "/api/packages/user5/pypi", body).
			SetHeader("Content-Type", w.FormDataContentType()).AddBasicAuth("user5"), http.StatusCreated)
	}

	v2token := func(user string) string {
		t.Helper()
		resp := MakeRequest(t, NewRequest(t, "GET", "/v2/token").AddBasicAuth(user), http.StatusOK)
		var tr struct {
			Token string `json:"token"`
		}
		DecodeJSON(t, resp, &tr)
		return tr.Token
	}
	owner, reader := v2token("user5"), v2token("user2")
	config := `{"architecture":"amd64","os":"linux"}`
	configDigest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(config)))
	manifest := `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"` +
		configDigest + `","size":` + fmt.Sprint(len(config)) + `},"layers":[]}`
	push := func(token, image string) {
		t.Helper()
		MakeRequest(t, NewRequestWithBody(t, "POST", "/v2/user5/"+image+"/blobs/uploads?digest="+configDigest, strings.NewReader(config)).
			AddTokenAuth(token), http.StatusCreated)
		MakeRequest(t, NewRequestWithBody(t, "PUT", "/v2/user5/"+image+"/manifests/latest", strings.NewReader(manifest)).
			SetHeader("Content-Type", "application/vnd.oci.image.manifest.v1+json").AddTokenAuth(token), http.StatusCreated)
	}
	push(owner, "secret")
	MakeRequest(t, NewRequest(t, "HEAD", "/v2/user5/secret/manifests/latest").AddTokenAuth(owner), http.StatusOK)

	// user4 (writes repo4) publishes packages linked to repo4, which user2 may read
	damastPutNpm(t, "user4", "@user5/repo4", "1.0.0", setting.AppURL+"user5/repo4", http.StatusCreated)
	push(v2token("user4"), "repo4")

	type answer struct {
		Status     int
		Type, Body string
	}
	ask := func(req *RequestWrapper) answer {
		t.Helper()
		resp := MakeRequest(t, req, NoExpectedStatus)
		return answer{resp.Code, resp.Header().Get("Content-Type"), resp.Body.String()}
	}
	basic := func(method, path string) *RequestWrapper { return NewRequest(t, method, path).AddBasicAuth("user2") }
	bearer := func(method, path string) *RequestWrapper { return NewRequest(t, method, path).AddTokenAuth(reader) }

	t.Run("per-package lists work for readable packages", func(t *testing.T) {
		resp := MakeRequest(t, basic("GET", "/api/packages/user5/npm/-/package/@user5%2Frepo4/dist-tags"), http.StatusOK)
		assert.JSONEq(t, `{"latest":"1.0.0"}`, resp.Body.String())
		resp = MakeRequest(t, bearer("GET", "/v2/user5/repo4/tags/list"), http.StatusOK)
		assert.Contains(t, resp.Body.String(), `"latest"`)
		MakeRequest(t, basic("GET", "/api/packages/user5/npm/@user5%2Frepo4/-/repo4-1.0.0.tgz"), http.StatusOK)
	})

	// a hidden package must not take an error path (500 + ERROR log) where a missing one doesn't
	lc, cleanup := test.NewLogChecker(log.DEFAULT, log.ERROR)
	defer cleanup()
	lc.Filter("does not exist").StopMark("damast hidden-looks-missing done")
	for _, c := range []struct {
		req  func(method, path string) *RequestWrapper
		path string // %s is "secret" or "missing"
	}{
		{basic, "/api/packages/user5/npm/@user5%%2F%s"},
		{basic, "/api/packages/user5/pypi/simple/%s"},
		{basic, "/api/packages/user5/maven/com/example/%s/maven-metadata.xml"},
		{basic, "/api/packages/user5/maven/com/example/%s/maven-metadata.xml.sha1"},
		{bearer, "/v2/user5/%s/blobs/" + configDigest},
		{bearer, "/v2/user5/%s/manifests/latest"},
		{basic, "/api/packages/user5/npm/@user5%%2F%[1]s/-/%[1]s-1.0.0.tgz"},
		{basic, "/api/packages/user5/npm/-/package/@user5%%2F%s/dist-tags"},
		{bearer, "/v2/user5/%s/tags/list"},
	} {
		want := http.StatusNotFound
		if strings.HasSuffix(c.path, "/dist-tags") {
			want = http.StatusOK // npm answers {} for unknown packages
		}
		for _, method := range []string{"GET", "HEAD"} {
			missing := ask(c.req(method, fmt.Sprintf(c.path, "missing")))
			if missing.Status == http.StatusMethodNotAllowed {
				continue
			}
			assert.Equal(t, want, missing.Status, "%s %s", method, c.path)
			assert.Equal(t, missing, ask(c.req(method, fmt.Sprintf(c.path, "secret"))), "%s %s", method, c.path)
		}
	}
	log.Error("damast hidden-looks-missing done")
	filtered, _ := lc.Check(5 * time.Second)
	assert.False(t, filtered[0], "hidden package logged an error")
}

// Forgejo makes archived repositories read-only; publishing through such a repository must stop too.
func TestDamastPackagesArchivedRepo(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	damastRepoWritersCreate(t, 5)
	ctx := db.DefaultContext
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: 4, Type: unit.TypePackages}))
	damastPutGeneric(t, "user4", "repo4", "1.0", http.StatusCreated)

	_, err := db.GetEngine(ctx).ID(4).Cols("is_archived").Update(&repo_model.Repository{IsArchived: true})
	require.NoError(t, err)
	damastPutGeneric(t, "user4", "repo4", "1.1", http.StatusForbidden)

	task := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: 47})
	task.RepoID, task.OwnerID, task.IsForkPullRequest = 4, 5, false
	task.GenerateToken()
	require.NoError(t, actions_model.UpdateTask(ctx, task, "repo_id", "owner_id", "is_fork_pull_request", "token_hash", "token_salt", "token_last_eight"))
	MakeRequest(t, NewRequestWithBody(t, "PUT", "/api/packages/user5/generic/ci/1.0/file.bin", bytes.NewReader([]byte("x"))).
		AddTokenAuth(task.Token), http.StatusForbidden)

	MakeRequest(t, NewRequest(t, "GET", "/api/packages/user5/generic/repo4/1.0/file.bin").AddBasicAuth("user4"), http.StatusOK)
}

// A public-only token can't reach private repositories elsewhere in Forgejo, so it can't publish through one.
func TestDamastPackagesPublicOnlyToken(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	damastRepoWritersCreate(t, 5)
	ctx := db.DefaultContext
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: 4, Type: unit.TypePackages}))
	session := loginUser(t, "user4")
	publicOnly := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopePublicOnly, auth_model.AccessTokenScopeWritePackage)
	full := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWritePackage)
	put := func(token, version string, status int) {
		t.Helper()
		u := fmt.Sprintf("/api/packages/user5/generic/repo4/%s/file.bin", version)
		MakeRequest(t, NewRequestWithBody(t, "PUT", u, bytes.NewReader([]byte("x"))).AddTokenAuth(token), status)
	}

	put(publicOnly, "1.0", http.StatusCreated) // public repo: fine
	_, err := db.GetEngine(ctx).ID(4).Cols("is_private").Update(&repo_model.Repository{IsPrivate: true})
	require.NoError(t, err)
	put(publicOnly, "1.1", http.StatusForbidden)
	put(full, "1.1", http.StatusCreated)

	// reading needs provisional access only for private owners, and Forgejo already turns public-only
	// tokens away there (reqPackageAccess); this guards that it stays so
	_, err = db.GetEngine(ctx).ID(5).Cols("visibility").Update(&user_model.User{Visibility: api.VisibleTypePrivate})
	require.NoError(t, err)
	MakeRequest(t, NewRequest(t, "GET", "/api/packages/user5/generic/repo4/1.0/file.bin").AddTokenAuth(publicOnly), http.StatusForbidden)
	MakeRequest(t, NewRequest(t, "GET", "/api/packages/user5/generic/repo4/1.0/file.bin").AddTokenAuth(full), http.StatusOK)
}

// Who may create new package names. By default only owner-level package writers, as in Forgejo: a new name is
// claimed for the whole owner (and lands in per-owner signed indexes like Debian's). With "repo_writers",
// repository writers and Actions runs may create names too, but only in registries GitHub Packages has.
// New versions of a package linked to the repository are always fine.
func TestDamastPackagesNewNames(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	ctx := db.DefaultContext
	require.NoError(t, db.Insert(ctx, &repo_model.RepoUnit{RepoID: 4, Type: unit.TypePackages}))
	task := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: 47})
	task.RepoID, task.OwnerID, task.IsForkPullRequest = 4, 5, false
	task.GenerateToken()
	require.NoError(t, actions_model.UpdateTask(ctx, task, "repo_id", "owner_id", "is_fork_pull_request", "token_hash", "token_salt", "token_last_eight"))

	deb := func(auth func(*RequestWrapper) *RequestWrapper, name, version string, status int) {
		t.Helper()
		MakeRequest(t, auth(NewRequestWithBody(t, "PUT", "/api/packages/user5/debian/pool/stable/main/upload",
			createDebianArchive(name, version, "amd64", "x"))), status)
	}
	byTask := func(r *RequestWrapper) *RequestWrapper { return r.AddTokenAuth(task.Token) }
	byOwner := func(r *RequestWrapper) *RequestWrapper { return r.AddBasicAuth("user5") }
	generic := func(auth func(*RequestWrapper) *RequestWrapper, name string, status int) {
		t.Helper()
		MakeRequest(t, auth(NewRequestWithBody(t, "PUT", "/api/packages/user5/generic/"+name+"/1.0/file.bin",
			bytes.NewReader([]byte("x")))), status)
	}
	byWriter := func(r *RequestWrapper) *RequestWrapper { return r.AddBasicAuth("user4") }
	packagesIndex := func() string {
		return MakeRequest(t, NewRequest(t, "GET", "/api/packages/user5/debian/dists/stable/main/binary-amd64/Packages"), NoExpectedStatus).Body.String()
	}

	settings := "/api/v1/damast/packages/user5/settings"
	ownerToken := getUserToken(t, "user5", auth_model.AccessTokenScopeWritePackage)
	newPackages := func() string {
		t.Helper()
		var s struct {
			NewPackages string `json:"new_packages"`
		}
		DecodeJSON(t, MakeRequest(t, NewRequest(t, "GET", settings).AddTokenAuth(ownerToken), http.StatusOK), &s)
		return s.NewPackages
	}

	t.Run("default: only owners create names", func(t *testing.T) {
		assert.Equal(t, "owners", newPackages())
		deb(byTask, "curl", "99.0", http.StatusForbidden) // "curl 99" in user5's signed apt repository
		generic(byTask, "ci", http.StatusForbidden)
		generic(byWriter, "repo4", http.StatusForbidden)
		_, err := packages_model.GetPackageByName(ctx, 5, packages_model.TypeDebian, "curl")
		require.ErrorIs(t, err, packages_model.ErrPackageNotExist)
		assert.NotContains(t, packagesIndex(), "Package: curl")
	})

	t.Run("new versions of a linked package are fine", func(t *testing.T) {
		deb(byOwner, "repo4", "1.0", http.StatusCreated)
		p, err := packages_model.GetPackageByName(ctx, 5, packages_model.TypeDebian, "repo4")
		require.NoError(t, err)
		require.EqualValues(t, 4, p.RepoID)
		deb(byTask, "repo4", "2.0", http.StatusCreated)
		assert.Contains(t, packagesIndex(), "Version: 2.0")
	})

	t.Run("repo_writers: names in GitHub's registries", func(t *testing.T) {
		MakeRequest(t, NewRequestWithJSON(t, "PATCH", settings, map[string]string{"new_packages": "repo_writers"}).
			AddTokenAuth(ownerToken), http.StatusOK)
		assert.Equal(t, "repo_writers", newPackages())

		generic(byTask, "ci", http.StatusCreated)
		generic(byWriter, "repo4", http.StatusCreated)
		deb(byTask, "curl", "99.0", http.StatusForbidden) // Debian isn't one of them
		assert.NotContains(t, packagesIndex(), "Package: curl")
	})

	t.Run("settings API", func(t *testing.T) {
		writerToken := getUserToken(t, "user4", auth_model.AccessTokenScopeWritePackage)
		MakeRequest(t, NewRequestWithJSON(t, "PATCH", settings, map[string]string{"new_packages": "owners"}).
			AddTokenAuth(writerToken), http.StatusForbidden)
		MakeRequest(t, NewRequest(t, "GET", settings).AddTokenAuth(writerToken), http.StatusForbidden)
		MakeRequest(t, NewRequestWithJSON(t, "PATCH", settings, map[string]string{"new_packages": "everyone"}).
			AddTokenAuth(ownerToken), http.StatusUnprocessableEntity)
		readToken := getUserToken(t, "user5", auth_model.AccessTokenScopeReadPackage)
		MakeRequest(t, NewRequestWithJSON(t, "PATCH", settings, map[string]string{"new_packages": "owners"}).
			AddTokenAuth(readToken), http.StatusForbidden)

		// organizations: org owners (user2 owns org3)
		orgSettings := "/api/v1/damast/packages/org3/settings"
		MakeRequest(t, NewRequestWithJSON(t, "PATCH", orgSettings, map[string]string{"new_packages": "repo_writers"}).
			AddTokenAuth(getUserToken(t, "user2", auth_model.AccessTokenScopeWritePackage)), http.StatusOK)
		allow, err := damast.RepoWritersMayCreatePackages(ctx, 3)
		require.NoError(t, err)
		assert.True(t, allow)
		MakeRequest(t, NewRequestWithJSON(t, "PATCH", orgSettings, map[string]string{"new_packages": "owners"}).
			AddTokenAuth(ownerToken), http.StatusForbidden)
	})

	t.Run("settings page", func(t *testing.T) {
		session := loginUser(t, "user5")
		doc := NewHTMLParser(t, session.MakeRequest(t, NewRequest(t, "GET", "/user/settings/packages"), http.StatusOK).Body)
		assert.True(t, doc.Find(`#damast-new-packages-repo-writers`).Is("[checked]"))
		session.MakeRequest(t, NewRequestWithValues(t, "POST", "/user/settings/packages/damast", map[string]string{
			"new_packages": "owners",
		}), http.StatusSeeOther)
		assert.Equal(t, "owners", newPackages())
		generic(byTask, "ci2", http.StatusForbidden)

		org := loginUser(t, "user2")
		doc = NewHTMLParser(t, org.MakeRequest(t, NewRequest(t, "GET", "/org/org3/settings/packages"), http.StatusOK).Body)
		assert.True(t, doc.Find(`#damast-new-packages-repo-writers`).Is("[checked]"))
		org.MakeRequest(t, NewRequestWithValues(t, "POST", "/org/org3/settings/packages/damast", map[string]string{
			"new_packages": "owners",
		}), http.StatusSeeOther)
		allow, err := damast.RepoWritersMayCreatePackages(ctx, 3)
		require.NoError(t, err)
		assert.False(t, allow)
	})
}

// Site admins pass reqPackageAccess for every owner; provisional access must not narrow that just because
// the admin also collaborates on a repository of the owner.
func TestDamastPackagesSiteAdmin(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	damastPrivateOwner(t)
	damastCollaborator(t, 4, 1, perm.AccessModeWrite) // user1 is site admin
	damastPutGeneric(t, "user5", "secret", "1.0", http.StatusCreated)

	MakeRequest(t, NewRequest(t, "GET", "/api/packages/user5/generic/secret/1.0/file.bin").AddBasicAuth("user1"), http.StatusOK)
	damastPutGeneric(t, "user1", "secret", "1.1", http.StatusCreated)
}

// A request decides access once per package: npm/Maven metadata list every version and file, and
// recomputing repository permissions for each of them multiplies the queries.
func TestDamastPackagesReadDecidedOncePerRequest(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	damastRepoWritersCreate(t, 5)
	damastPrivateOwner(t)
	damastPutGeneric(t, "user4", "repo4", "1.0", http.StatusCreated)
	ctx := db.DefaultContext
	p, err := packages_model.GetPackageByName(ctx, 5, packages_model.TypeGeneric, "repo4")
	require.NoError(t, err)

	user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	request := func() gocontext.Context {
		return gocontext.WithValue(ctx, damast_packages.GrantKey, &damast_packages.Grant{Mode: perm.AccessModeRead, Doer: user2})
	}
	rctx := request()
	require.NoError(t, damast_packages.CheckRead(rctx, p))

	_, err = db.GetEngine(ctx).Where("repo_id = 4 AND user_id = 2").Delete(&repo_model.Collaboration{})
	require.NoError(t, err)
	_, err = db.GetEngine(ctx).Where("repo_id = 4 AND user_id = 2").Delete(&access_model.Access{})
	require.NoError(t, err)
	require.NoError(t, damast_packages.CheckRead(rctx, p), "same request: decided once")
	require.ErrorIs(t, damast_packages.CheckRead(request(), p), packages_model.ErrPackageNotExist, "next request: decided again")
}

// An Actions run whose repository was deleted gets plain Forgejo rules, not an error.
func TestDamastPackagesActionsDeletedRepo(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	task := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: 47})
	task.RepoID, task.OwnerID, task.IsForkPullRequest = 9999, 5, false
	task.GenerateToken()
	require.NoError(t, actions_model.UpdateTask(db.DefaultContext, task, "repo_id", "owner_id", "is_fork_pull_request", "token_hash", "token_salt", "token_last_eight"))
	MakeRequest(t, NewRequestWithBody(t, "PUT", "/api/packages/user5/generic/ci/1.0/file.bin", bytes.NewReader([]byte("x"))).
		AddTokenAuth(task.Token), http.StatusUnauthorized)
}

// conan checks the server before it uploads (recipe snapshot, digest, download URLs, revisions); repo
// writers of a private owner must get through those, and hidden recipes answer like missing ones.
func TestDamastPackagesConanPrivateOwner(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	damastPrivateOwner(t) // user4 writes repo4, user2 reads it
	base := setting.AppURL + "api/packages/user5/conan"
	token := func(user string) string {
		t.Helper()
		resp := MakeRequest(t, NewRequest(t, "GET", base+"/v1/users/authenticate").AddBasicAuth(user), http.StatusOK)
		return resp.Body.String()
	}
	writer, reader, owner := token("user4"), token("user2"), token("user5")
	damastRepoWritersCreate(t, 5) // still: conan isn't a GitHub registry, so new names stay owner-only

	// the owner creates the name (linked to repo4 by name), the repo writer publishes new versions with
	// upstream's flows, including their pre-upload GETs that expect 404
	uploadConanPackageV2(t, base, owner, "repo4", "0.1", "dev", "stable", "rev1", "prev1")
	uploadConanPackageV1(t, base, writer, "repo4", "1.0", "dev", "stable")
	uploadConanPackageV2(t, base, writer, "repo4", "2.0", "dev", "stable", "rev1", "prev1")
	p, err := packages_model.GetPackageByName(db.DefaultContext, 5, packages_model.TypeConan, "repo4")
	require.NoError(t, err)
	assert.EqualValues(t, 4, p.RepoID)
	user5 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	damastCollaborator(t, damastRepo(t, user5, "tool", true).ID, 4, perm.AccessModeWrite) // a repo user4 writes, named like the new package
	MakeRequest(t, NewRequestWithBody(t, "PUT", base+"/v2/conans/tool/1.0/dev/stable/revisions/rev1/files/"+conanfileName,
		strings.NewReader(buildConanfileContent("tool", "1.0"))).AddTokenAuth(writer), http.StatusForbidden)

	uploadConanPackageV2(t, base, owner, "secret", "1.0", "dev", "stable", "rev1", "prev1")

	get := func(tok, path string, status int) string {
		t.Helper()
		return MakeRequest(t, NewRequest(t, "GET", base+path).AddTokenAuth(tok), status).Body.String()
	}
	get(reader, "/v1/conans/repo4/1.0/dev/stable", http.StatusOK)
	get(reader, "/v2/conans/repo4/2.0/dev/stable/latest", http.StatusOK)
	get(reader, "/v2/conans/repo4/2.0/dev/stable/revisions/rev1/files/"+conanfileName, http.StatusOK)

	for _, path := range []string{
		"/v1/conans/%s/1.0/dev/stable",
		"/v1/conans/%s/1.0/dev/stable/digest",
		"/v1/conans/%s/1.0/dev/stable/download_urls",
		"/v1/conans/%s/1.0/dev/stable/search",
		"/v1/conans/%s/1.0/dev/stable/packages/" + conanPackageReference,
		"/v2/conans/%s/1.0/dev/stable/latest",
		"/v2/conans/%s/1.0/dev/stable/revisions",
		"/v2/conans/%s/1.0/dev/stable/revisions/rev1/files",
		"/v2/conans/%s/1.0/dev/stable/revisions/rev1/files/" + conanfileName,
		"/v2/conans/%s/1.0/dev/stable/revisions/rev1/search",
		"/v2/conans/%s/1.0/dev/stable/revisions/rev1/packages/" + conanPackageReference + "/latest",
	} {
		missing := get(reader, fmt.Sprintf(path, "missing"), http.StatusNotFound)
		assert.Equal(t, missing, get(reader, fmt.Sprintf(path, "secret"), http.StatusNotFound), path)
	}

	// the owner-wide recipe search keeps Forgejo's owner rules; readers never write
	get(reader, "/v2/conans/search?q=secret", http.StatusUnauthorized)
	MakeRequest(t, NewRequestWithBody(t, "PUT", base+"/v2/conans/repo4/3.0/dev/stable/revisions/rev1/files/"+conanfileName,
		strings.NewReader(buildConanfileContent("repo4", "3.0"))).AddTokenAuth(reader), http.StatusUnauthorized)
}

// pub publishes in three steps (GET upload URL, POST file, GET finalize); repo writers get all of them,
// with the same per-package checks as single-step uploads.
func TestDamastPackagesPubUpload(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	require.NoError(t, db.Insert(db.DefaultContext, &repo_model.RepoUnit{RepoID: 4, Type: unit.TypePackages}))
	archive := func(name, version string) []byte {
		pubspec := "name: " + name + "\nversion: " + version + "\ndescription: d"
		var buf bytes.Buffer
		zw := gzip.NewWriter(&buf)
		tw := tar.NewWriter(zw)
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: "pubspec.yaml", Mode: 0o600, Size: int64(len(pubspec))}))
		_, _ = tw.Write([]byte(pubspec))
		require.NoError(t, tw.Close())
		require.NoError(t, zw.Close())
		return buf.Bytes()
	}
	publish := func(user, name, version string, status int) *http.Response {
		t.Helper()
		resp := MakeRequest(t, NewRequest(t, "GET", "/api/packages/user5/pub/api/packages/versions/new").AddBasicAuth(user), NoExpectedStatus)
		if resp.Code != http.StatusOK {
			require.Equal(t, status, resp.Code)
			return resp.Result()
		}
		var upload struct {
			URL string `json:"url"`
		}
		DecodeJSON(t, resp, &upload)
		body := &bytes.Buffer{}
		w := multipart.NewWriter(body)
		part, _ := w.CreateFormFile("file", "dummy.tar.gz")
		_, _ = part.Write(archive(name, version))
		require.NoError(t, w.Close())
		resp = MakeRequest(t, NewRequestWithBody(t, "POST", upload.URL, body).
			SetHeader("Content-Type", w.FormDataContentType()).AddBasicAuth(user), NoExpectedStatus)
		if resp.Code != http.StatusNoContent {
			require.Equal(t, status, resp.Code)
			return resp.Result()
		}
		return MakeRequest(t, NewRequest(t, "GET", resp.Header().Get("Location")).AddBasicAuth(user), status).Result()
	}

	damastRepoWritersCreate(t, 5)                     // still: pub isn't a GitHub registry, so new names stay owner-only
	publish("user5", "repo4", "1.0.0", http.StatusOK) // the owner creates the name, linked to repo4
	p, err := packages_model.GetPackageByName(db.DefaultContext, 5, packages_model.TypePub, "repo4")
	require.NoError(t, err)
	assert.EqualValues(t, 4, p.RepoID)

	user5 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	damastCollaborator(t, damastRepo(t, user5, "tool", true).ID, 4, perm.AccessModeWrite)
	publish("user4", "repo4", "1.0.1", http.StatusOK)           // user4 writes repo4: new versions are fine
	publish("user4", "tool", "1.0.0", http.StatusForbidden)     // new names are not, even with a repo "tool" user4 writes
	publish("user2", "repo4", "1.0.2", http.StatusUnauthorized) // no repository write at all

	// finalize checks the package like an upload would
	publish("user5", "secret", "1.0.0", http.StatusOK)
	MakeRequest(t, NewRequest(t, "GET", "/api/packages/user5/pub/api/packages/versions/new/finalize/secret/1.0.0").
		AddBasicAuth("user4"), http.StatusForbidden)
	MakeRequest(t, NewRequest(t, "GET", "/api/packages/user5/pub/api/packages/versions/new/finalize/repo4/1.0.1").
		AddBasicAuth("user4"), http.StatusOK)
}
