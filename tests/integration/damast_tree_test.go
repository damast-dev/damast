// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"forgejo.org/models/db"
	"forgejo.org/models/organization"
	repo_model "forgejo.org/models/repo"
	unit_model "forgejo.org/models/unit"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/git"
	org_service "forgejo.org/services/org"
	repo_service "forgejo.org/services/repository"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type damastTreeResponse struct {
	Dirs []struct {
		Path    string `json:"path"`
		Entries []struct {
			Name string `json:"name"`
			Path string `json:"path"`
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"entries"`
		More int `json:"more"`
	} `json:"dirs"`
}

func (r damastTreeResponse) summary() map[string][]string {
	out := map[string][]string{}
	for _, d := range r.Dirs {
		out[d.Path] = []string{}
		for _, e := range d.Entries {
			out[d.Path] = append(out[d.Path], e.Type+":"+e.Path)
		}
	}
	return out
}

func TestDamastTreeView(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, _ *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		var sha string
		repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Files: forgery.MapFS{
				"README.md":        forgery.MapFile("hi"),
				"b.txt":            forgery.MapFile("b"),
				"src/main.go":      forgery.MapFile("package main"),
				"src/lib/deep.go":  forgery.MapFile("package lib"),
				"src/lib/z/z.go":   forgery.MapFile("package z"),
				"link":             forgery.MapSymlink("src/main.go"),
				"module":           forgery.MapSubmodule("95601d16476a"),
				".gitmodules":      forgery.MapFile("[submodule \"module\"]\n  path = module\n  url = https://git.example.org/sub.git\n"),
				"Alpha/x.txt":      forgery.MapFile("x"),
				"src/lib/10.go":    forgery.MapFile("x"),
				"src/lib/9.go":     forgery.MapFile("x"),
				"src/lib/a b#c.go": forgery.MapFile("x"),
			},
			LatestSha: &sha,
		})
		_, _, err := git.NewCommand(git.DefaultContext, "tag", "v1").AddDynamicArguments(sha).RunStdString(&git.RunOpts{Dir: repo.RepoPath()})
		require.NoError(t, err)

		base := "/" + repo.FullName() + "/tree-view/"
		get := func(t *testing.T, path string) damastTreeResponse {
			t.Helper()
			resp := MakeRequest(t, NewRequest(t, "GET", base+path), http.StatusOK)
			var r damastTreeResponse
			DecodeJSON(t, resp, &r)
			return r
		}

		t.Run("Root", func(t *testing.T) {
			// same order as the code view: directories and submodules first, natural sort
			r := get(t, "branch/main")
			assert.Equal(t, map[string][]string{"": {
				"dir:Alpha", "submodule:module", "dir:src", "file:.gitmodules", "file:b.txt", "symlink:link", "file:README.md",
			}}, r.summary())
			for _, e := range r.Dirs[0].Entries {
				if e.Type == "submodule" {
					assert.Equal(t, "https://git.example.org/sub", e.URL)
				} else {
					assert.Empty(t, e.URL)
				}
			}
		})

		t.Run("Nested", func(t *testing.T) {
			assert.Equal(t, map[string][]string{"src/lib": {
				"dir:src/lib/z", "file:src/lib/9.go", "file:src/lib/10.go", "file:src/lib/a b#c.go", "file:src/lib/deep.go",
			}}, get(t, "branch/main/src/lib").summary())
			assert.Len(t, get(t, "branch/main/src/lib/a%20b%23c.go?ancestors=1").Dirs, 3)
		})

		t.Run("Ancestors", func(t *testing.T) {
			r := get(t, "branch/main/src/lib/z/z.go?ancestors=1")
			paths := []string{}
			for _, d := range r.Dirs {
				paths = append(paths, d.Path)
			}
			assert.Equal(t, []string{"", "src", "src/lib", "src/lib/z"}, paths)

			// a directory includes its own listing, the root alone only the root
			assert.Len(t, get(t, "branch/main/src?ancestors=1").Dirs, 2)
			assert.Len(t, get(t, "branch/main?ancestors=1").Dirs, 1)
		})

		t.Run("FilePath", func(t *testing.T) {
			MakeRequest(t, NewRequest(t, "GET", base+"branch/main/src/main.go"), http.StatusNotFound)
			MakeRequest(t, NewRequest(t, "GET", base+"branch/main/link"), http.StatusNotFound)
			MakeRequest(t, NewRequest(t, "GET", base+"branch/main/module"), http.StatusNotFound)
		})

		t.Run("TagAndCommit", func(t *testing.T) {
			want := get(t, "branch/main/src").summary()
			assert.Equal(t, want, get(t, "tag/v1/src").summary())
			assert.Equal(t, want, get(t, "commit/"+sha+"/src").summary())
		})

		t.Run("NotFound", func(t *testing.T) {
			MakeRequest(t, NewRequest(t, "GET", base+"branch/main/nope"), http.StatusNotFound)
			MakeRequest(t, NewRequest(t, "GET", base+"branch/main/nope?ancestors=1"), http.StatusNotFound)
			MakeRequest(t, NewRequest(t, "GET", base+"branch/nope"), http.StatusNotFound)
			MakeRequest(t, NewRequest(t, "GET", base+"tag/nope/src"), http.StatusNotFound)
		})

		t.Run("Private", func(t *testing.T) {
			private := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
				IsPrivate: true,
				Files:     forgery.MapFS{"secret.txt": forgery.MapFile("s")},
			})
			treeURL := "/" + private.FullName() + "/tree-view/branch/main"
			codeURL := "/" + private.FullName() + "/src/branch/main"
			MakeRequest(t, NewRequest(t, "GET", treeURL), http.StatusNotFound)
			MakeRequest(t, NewRequest(t, "GET", codeURL), http.StatusNotFound)
			session := loginUser(t, "user4")
			session.MakeRequest(t, NewRequest(t, "GET", treeURL), http.StatusNotFound)
			session.MakeRequest(t, NewRequest(t, "GET", codeURL), http.StatusNotFound)
			loginUser(t, "user2").MakeRequest(t, NewRequest(t, "GET", treeURL), http.StatusOK)
		})

		t.Run("Empty", func(t *testing.T) {
			empty := forgery.CreateRepository(t, user2, nil)
			MakeRequest(t, NewRequest(t, "GET", "/"+empty.FullName()+"/tree-view/branch/main"), http.StatusNotFound)
		})

		t.Run("Cap", func(t *testing.T) {
			files := forgery.MapFS{}
			for i := range 1003 {
				files[fmt.Sprintf("f%04d", i)] = forgery.MapFile("x")
			}
			big := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{Files: files})
			resp := MakeRequest(t, NewRequest(t, "GET", "/"+big.FullName()+"/tree-view/branch/main"), http.StatusOK)
			var r damastTreeResponse
			DecodeJSON(t, resp, &r)
			assert.Len(t, r.Dirs[0].Entries, 1000)
			assert.Equal(t, 3, r.Dirs[0].More)
		})

		t.Run("CodeViewHook", func(t *testing.T) {
			doc := NewHTMLParser(t, MakeRequest(t, NewRequest(t, "GET", "/"+repo.FullName()+"/src/branch/main/src/lib"), http.StatusOK).Body)
			nav := doc.Find("#damast-file-tree")
			assert.Equal(t, 1, nav.Length())
			v, _ := nav.Attr("data-tree-path")
			assert.Equal(t, "src/lib", v)
			v, _ = nav.Attr("data-ref-sub-url")
			assert.Equal(t, "branch/main", v)
		})
	})
}

// The tree must not show more than the code view: nothing to people who can read issues but not code.
// /tree-list (upstream, used by "Go to file") is checked too, it had no code permission check.
func TestDamastTreeViewCodeAccess(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, _ *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		urls := func(fullName, ref string) []string {
			return []string{"/" + fullName + "/tree-view/branch/" + ref, "/" + fullName + "/tree-list/branch/" + ref}
		}

		t.Run("CodeUnitDisabled", func(t *testing.T) {
			repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
				Files: forgery.MapFS{"secret-name.txt": forgery.MapFile("s")},
			})
			require.NoError(t, repo_service.UpdateRepositoryUnits(db.DefaultContext, repo, nil, []unit_model.Type{unit_model.TypeCode}))
			session := loginUser(t, "user4")
			for _, u := range urls(repo.FullName(), "main") {
				MakeRequest(t, NewRequest(t, "GET", u), http.StatusNotFound)
				session.MakeRequest(t, NewRequest(t, "GET", u), http.StatusNotFound)
			}
		})

		t.Run("IssuesOnlyTeam", func(t *testing.T) {
			// team 7 of org3 can only read issues; user15 is its only member
			team := unittest.AssertExistsAndLoadBean(t, &organization.Team{ID: 7})
			repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{OwnerName: "org3", LowerName: "repo3"})
			require.NoError(t, org_service.TeamAddRepository(db.DefaultContext, team, repo))
			defer func() { require.NoError(t, repo_service.RemoveRepositoryFromTeam(db.DefaultContext, team, repo.ID)) }()
			session := loginUser(t, "user15")
			session.MakeRequest(t, NewRequest(t, "GET", "/org3/repo3/issues"), http.StatusOK)
			for _, u := range urls("org3/repo3", "master") {
				session.MakeRequest(t, NewRequest(t, "GET", u), http.StatusNotFound)
			}
		})
	})
}

func TestDamastTreeViewPaths(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, _ *url.URL) {
		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		repo := forgery.CreateRepository(t, user2, &forgery.CreateRepositoryOptions{
			Files: forgery.MapFS{
				"src/a.go":                      forgery.MapFile("x"),
				`<img src=x onerror=alert(1)>"'`: forgery.MapFile("x"),
				"out":                           forgery.MapSymlink("../../../etc"),
			},
		})
		base := "/" + repo.FullName() + "/tree-view/branch/main"
		for _, p := range []string{"/src/..", "/src/%2e%2e", "/..", "/%2e%2e/%2e%2e", "/src%2F..%2F..", "/out", "/out/passwd"} {
			resp := MakeRequest(t, NewRequest(t, "GET", base+p), NoExpectedStatus)
			assert.Contains(t, []int{http.StatusOK, http.StatusNotFound}, resp.Code, p)
			if resp.Code == http.StatusOK {
				var r damastTreeResponse
				DecodeJSON(t, resp, &r)
				for _, d := range r.Dirs {
					assert.NotContains(t, d.Path, "..", p)
				}
			}
		}
		// names are data: the JSON carries them verbatim, Vue renders them as text
		resp := MakeRequest(t, NewRequest(t, "GET", base), http.StatusOK)
		assert.Contains(t, resp.Header().Get("Content-Type"), "application/json")
		var r damastTreeResponse
		DecodeJSON(t, resp, &r)
		assert.Equal(t, map[string][]string{"": {"dir:src", "file:<img src=x onerror=alert(1)>\"'", "symlink:out"}}, r.summary())
	})
}
