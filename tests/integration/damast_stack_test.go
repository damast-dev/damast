// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	auth_model "forgejo.org/models/auth"
	git_model "forgejo.org/models/git"
	issues_model "forgejo.org/models/issues"
	"forgejo.org/models/perm"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/gitrepo"
	api "forgejo.org/modules/structs"
	app_context "forgejo.org/services/context"
	pull_service "forgejo.org/services/pull"
	"forgejo.org/services/repository/commitstatus"
	files_service "forgejo.org/services/repository/files"
	"forgejo.org/tests/forgery"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type damastStackEnv struct {
	t     *testing.T
	owner *user_model.User
	repo  *repo_model.Repository
	token string
}

func newDamastStackEnv(t *testing.T, private bool, collaborators map[*user_model.User]perm.AccessMode) *damastStackEnv {
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	repo := forgery.CreateRepository(t, owner, &forgery.CreateRepositoryOptions{
		Files: forgery.FilesInit{Readme: "Default"}, IsPrivate: private, Collaborators: collaborators,
	})
	token := getTokenForLoggedInUser(t, loginUser(t, owner.Name), auth_model.AccessTokenScopeWriteRepository)
	return &damastStackEnv{t: t, owner: owner, repo: repo, token: token}
}

// commit writes path on newBranch, starting from oldBranch.
func (e *damastStackEnv) commit(oldBranch, newBranch, path, content string) {
	e.t.Helper()
	op, sha := "create", e.fileSHA(oldBranch, path)
	if sha != "" {
		op = "update"
	}
	id := &files_service.IdentityOptions{Name: e.owner.Name, Email: e.owner.Email}
	_, err := files_service.ChangeRepoFiles(e.t.Context(), e.repo, e.owner, &files_service.ChangeRepoFilesOptions{
		Files:     []*files_service.ChangeRepoFile{{Operation: op, SHA: sha, TreePath: path, ContentReader: strings.NewReader(content)}},
		Message:   "edit " + path + " on " + newBranch,
		OldBranch: oldBranch, NewBranch: newBranch, Author: id, Committer: id,
		Dates: &files_service.CommitDateOptions{Author: time.Now(), Committer: time.Now()},
	})
	require.NoError(e.t, err)
}

func (e *damastStackEnv) headOf(branch string) (string, error) {
	gitRepo, err := gitrepo.OpenRepository(e.t.Context(), e.repo)
	require.NoError(e.t, err)
	defer gitRepo.Close()
	return gitRepo.GetBranchCommitID(branch)
}

func (e *damastStackEnv) hasFile(branch, path string) bool {
	return e.fileSHA(branch, path) != ""
}

func (e *damastStackEnv) fileSHA(branch, path string) string {
	gitRepo, err := gitrepo.OpenRepository(e.t.Context(), e.repo)
	require.NoError(e.t, err)
	defer gitRepo.Close()
	c, err := gitRepo.GetBranchCommit(branch)
	require.NoError(e.t, err)
	entry, err := c.GetTreeEntryByPath(path)
	if err != nil {
		return ""
	}
	return entry.ID.String()
}

// layer creates branch head on top of base with one file and opens a PR head -> base.
func (e *damastStackEnv) layer(head, base, title string) *issues_model.PullRequest {
	e.t.Helper()
	e.commit(base, head, head+".txt", head)
	issue := &issues_model.Issue{RepoID: e.repo.ID, Title: title, PosterID: e.owner.ID, Poster: e.owner, IsPull: true}
	pr := &issues_model.PullRequest{
		HeadRepoID: e.repo.ID, BaseRepoID: e.repo.ID, HeadBranch: head, BaseBranch: base,
		HeadRepo: e.repo, BaseRepo: e.repo, Type: issues_model.PullRequestGitea,
	}
	require.NoError(e.t, pull_service.NewPullRequest(e.t.Context(), e.repo, issue, nil, nil, pr, nil))
	return pr
}

func (e *damastStackEnv) pr(pr *issues_model.PullRequest) *issues_model.PullRequest {
	return unittest.AssertExistsAndLoadBean(e.t, &issues_model.PullRequest{ID: pr.ID})
}

func (e *damastStackEnv) url(pr *issues_model.PullRequest, suffix string) string {
	return fmt.Sprintf("/api/v1/repos/%s/%s/pulls/%d/damast/stack%s", e.owner.Name, e.repo.Name, pr.Index, suffix)
}

func (e *damastStackEnv) stack(pr *issues_model.PullRequest, token string, status int) *api.DamastStack {
	e.t.Helper()
	req := NewRequest(e.t, "GET", e.url(pr, ""))
	if token != "" {
		req.AddTokenAuth(token)
	}
	resp := MakeRequest(e.t, req, status)
	if status != http.StatusOK {
		return nil
	}
	var s api.DamastStack
	DecodeJSON(e.t, resp, &s)
	return &s
}

// action posts rebase or merge with the heads currently shown by the API, after applying override.
func (e *damastStackEnv) action(pr *issues_model.PullRequest, action, token string, status int, override map[string]string) *api.DamastStackActionResult {
	e.t.Helper()
	heads := map[string]string{}
	for _, l := range e.stack(pr, e.token, http.StatusOK).Layers {
		heads[fmt.Sprint(l.Number)] = l.HeadSHA
	}
	for k, v := range override {
		heads[k] = v
	}
	req := NewRequestWithJSON(e.t, "POST", e.url(pr, "/"+action), &api.DamastStackActionOption{Heads: heads}).AddTokenAuth(token)
	var res api.DamastStackActionResult
	DecodeJSON(e.t, MakeRequest(e.t, req, status), &res)
	return &res
}

func TestDamastStack(t *testing.T) {
	onApplicationRun(t, func(t *testing.T, _ *url.URL) {
		t.Run("API and stack map", func(t *testing.T) {
			e := newDamastStackEnv(t, false, nil)
			a := e.layer("a", "main", "Layer A")
			b := e.layer("b", "a", "WIP: Layer B")
			c := e.layer("c", "b", "Layer C")
			d := e.layer("d", "a", "Layer D")
			sha, err := e.headOf("c")
			require.NoError(t, err)
			require.NoError(t, commitstatus.CreateCommitStatus(t.Context(), e.repo, e.owner, sha, &git_model.CommitStatus{State: api.CommitStatusSuccess, Context: "ci"}))

			s := e.stack(c, "", http.StatusOK)
			assert.Equal(t, "main", s.Trunk)
			assert.Equal(t, c.Index, s.Current)
			require.Len(t, s.Layers, 4)
			var got []string
			for _, l := range s.Layers {
				got = append(got, fmt.Sprintf("%s<%d:%d", l.Head, l.Parent, l.Depth))
			}
			assert.Equal(t, []string{"a<0:0", fmt.Sprintf("b<%d:1", a.Index), fmt.Sprintf("c<%d:2", b.Index), fmt.Sprintf("d<%d:1", a.Index)}, got)
			assert.True(t, s.Layers[1].Draft)
			assert.Equal(t, api.CommitStatusSuccess, s.Layers[2].Checks)
			assert.Equal(t, "open", s.Layers[2].State)
			assert.Equal(t, sha, s.Layers[2].HeadSHA)

			// stack map in the PR page, on every tab
			session := loginUser(t, e.owner.Name)
			for _, tab := range []string{"", "/files", "/commits"} {
				resp := session.MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("/%s/%s/pulls/%d%s", e.owner.Name, e.repo.Name, d.Index, tab)), http.StatusOK)
				doc := NewHTMLParser(t, resp.Body)
				assert.Equal(t, 4, doc.Find("#damast-stack .damast-stack-layer").Length(), tab)
				assert.Equal(t, fmt.Sprint(d.Index), doc.Find("#damast-stack [aria-current]").AttrOr("data-index", ""), tab)
				assert.Equal(t, 1, doc.Find("#damast-stack-rebase").Length(), tab)
				assert.Equal(t, 1, doc.Find("#damast-stack-merge").Length(), tab)
			}
			// the bottom layer has nothing to merge "up to"
			resp := session.MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("/%s/%s/pulls/%d", e.owner.Name, e.repo.Name, a.Index)), http.StatusOK)
			assert.Equal(t, 0, NewHTMLParser(t, resp.Body).Find("#damast-stack-merge").Length())
		})

		t.Run("visibility", func(t *testing.T) {
			reader := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
			e := newDamastStackEnv(t, true, map[*user_model.User]perm.AccessMode{reader: perm.AccessModeRead})
			e.layer("a", "main", "Layer A")
			b := e.layer("b", "a", "Layer B")

			e.stack(b, "", http.StatusNotFound)
			stranger := getTokenForLoggedInUser(t, loginUser(t, "user5"), auth_model.AccessTokenScopeReadRepository)
			e.stack(b, stranger, http.StatusNotFound)
			readerToken := getTokenForLoggedInUser(t, loginUser(t, reader.Name), auth_model.AccessTokenScopeWriteRepository)
			assert.Len(t, e.stack(b, readerToken, http.StatusOK).Layers, 2)

			// a reader sees the map, but no buttons, and cannot run the actions
			resp := loginUser(t, reader.Name).MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("/%s/%s/pulls/%d", e.owner.Name, e.repo.Name, b.Index)), http.StatusOK)
			doc := NewHTMLParser(t, resp.Body)
			assert.Equal(t, 2, doc.Find("#damast-stack .damast-stack-layer").Length())
			assert.Equal(t, 0, doc.Find("#damast-stack-rebase, #damast-stack-merge").Length())
			e.commit("main", "main", "main.txt", "moved")
			res := e.action(b, "rebase", readerToken, http.StatusForbidden, nil)
			assert.Empty(t, res.Done)
			res = e.action(b, "merge", readerToken, http.StatusConflict, nil)
			assert.Empty(t, res.Done)
			assert.False(t, e.pr(b).HasMerged)
		})

		t.Run("retarget after merge when the branch is kept", func(t *testing.T) {
			e := newDamastStackEnv(t, false, nil)
			a := e.layer("a", "main", "Layer A")
			b := e.layer("b", "a", "Layer B")
			session := loginUser(t, e.owner.Name)
			testPullMerge(t, session, e.owner.Name, e.repo.Name, fmt.Sprint(a.Index), repo_model.MergeStyleMerge, false)

			assert.True(t, e.pr(a).HasMerged)
			assert.Equal(t, "main", e.pr(b).BaseBranch)
			_, err := e.headOf("a")
			require.NoError(t, err, "branch a is kept")
			unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{IssueID: b.IssueID, Type: issues_model.CommentTypeChangeTargetBranch, OldRef: "a", NewRef: "main"})

			// a protected head branch is long-lived: PRs aimed at it stay
			c := e.layer("develop", "main", "Develop")
			f := e.layer("feature", "develop", "Feature")
			require.NoError(t, git_model.UpdateProtectBranch(t.Context(), e.repo, &git_model.ProtectedBranch{RepoID: e.repo.ID, RuleName: "develop", CanPush: true}, git_model.WhitelistOptions{}))
			testPullMerge(t, session, e.owner.Name, e.repo.Name, fmt.Sprint(c.Index), repo_model.MergeStyleMerge, false)
			assert.True(t, e.pr(c).HasMerged)
			assert.Equal(t, "develop", e.pr(f).BaseBranch)
		})

		t.Run("rebase stack", func(t *testing.T) {
			e := newDamastStackEnv(t, false, nil)
			a := e.layer("a", "main", "Layer A")
			b := e.layer("b", "a", "Layer B")
			c := e.layer("c", "b", "Layer C")
			e.commit("main", "main", "main.txt", "moved on")

			res := e.action(b, "rebase", e.token, http.StatusOK, nil)
			assert.Equal(t, []int64{a.Index, b.Index, c.Index}, res.Done)
			for _, br := range []string{"a", "b", "c"} {
				assert.True(t, e.hasFile(br, "main.txt"), br)
			}
			assert.True(t, e.hasFile("c", "a.txt") && e.hasFile("c", "b.txt") && e.hasFile("c", "c.txt"))
			res = e.action(b, "rebase", e.token, http.StatusOK, nil)
			assert.Empty(t, res.Done, "up to date")

			t.Run("moved branch", func(t *testing.T) {
				e.commit("main", "main", "main.txt", "moved again")
				cBefore, _ := e.headOf("c")
				res := e.action(a, "rebase", e.token, http.StatusConflict, map[string]string{fmt.Sprint(b.Index): cBefore})
				assert.Equal(t, []int64{a.Index}, res.Done)
				assert.Equal(t, b.Index, res.FailedAt)
				assert.Contains(t, res.Message, "changed since the page was loaded")
				cAfter, _ := e.headOf("c")
				assert.Equal(t, cBefore, cAfter, "layers above the failure are untouched")
			})

			t.Run("conflict", func(t *testing.T) {
				e.commit("b", "b", "README.md", "b's readme")
				e.commit("main", "main", "README.md", "main's readme")
				bBefore, _ := e.headOf("b")
				res := e.action(c, "rebase", e.token, http.StatusConflict, nil)
				assert.Equal(t, []int64{a.Index}, res.Done)
				assert.Equal(t, b.Index, res.FailedAt)
				assert.Contains(t, res.Message, "conflict")
				bAfter, _ := e.headOf("b")
				assert.Equal(t, bBefore, bAfter)
			})

			t.Run("protected branch", func(t *testing.T) {
				require.NoError(t, git_model.UpdateProtectBranch(t.Context(), e.repo, &git_model.ProtectedBranch{RepoID: e.repo.ID, RuleName: "c", CanPush: true}, git_model.WhitelistOptions{}))
				res := e.action(a, "rebase", e.token, http.StatusForbidden, nil)
				assert.Empty(t, res.Done)
			})
		})

		t.Run("web rebase", func(t *testing.T) {
			e := newDamastStackEnv(t, false, nil)
			e.layer("a", "main", "Layer A")
			b := e.layer("b", "a", "Layer B")
			e.commit("main", "main", "main.txt", "moved on")
			session := loginUser(t, e.owner.Name)
			link := fmt.Sprintf("/%s/%s/pulls/%d", e.owner.Name, e.repo.Name, b.Index)
			doc := NewHTMLParser(t, session.MakeRequest(t, NewRequest(t, "GET", link), http.StatusOK).Body)
			heads := doc.Find("#damast-stack form input[name=heads]").AttrOr("value", "")
			session.MakeRequest(t, NewRequestWithValues(t, "POST", link+"/damast/stack/rebase", map[string]string{
				"heads": heads,
			}), http.StatusSeeOther)
			assert.True(t, e.hasFile("b", "main.txt"))
			flash := session.GetCookie(app_context.CookieNameFlash)
			require.NotNil(t, flash)
			assert.Contains(t, flash.Value, "success%3DRebased")
		})

		t.Run("merge up to here", func(t *testing.T) {
			e := newDamastStackEnv(t, false, nil)
			a := e.layer("a", "main", "Layer A")
			b := e.layer("b", "a", "Layer B")
			c := e.layer("c", "b", "Layer C")
			res := e.action(b, "merge", e.token, http.StatusOK, nil)
			assert.Equal(t, []int64{a.Index, b.Index}, res.Done)
			assert.True(t, e.pr(a).HasMerged)
			assert.True(t, e.pr(b).HasMerged)
			assert.Equal(t, "main", e.pr(b).BaseBranch)
			assert.False(t, e.pr(c).HasMerged)
			assert.Equal(t, "main", e.pr(c).BaseBranch, "retargeted onto the trunk")
			assert.True(t, e.hasFile("main", "b.txt"))
		})

		t.Run("merge up to here stops at a draft", func(t *testing.T) {
			e := newDamastStackEnv(t, false, nil)
			a := e.layer("a", "main", "Layer A")
			b := e.layer("b", "a", "WIP: Layer B")
			c := e.layer("c", "b", "Layer C")
			res := e.action(c, "merge", e.token, http.StatusConflict, nil)
			assert.Equal(t, []int64{a.Index}, res.Done)
			assert.Equal(t, b.Index, res.FailedAt)
			assert.False(t, e.pr(b).HasMerged)
			assert.False(t, e.pr(c).HasMerged)
		})

		t.Run("merge up to here stops at pending checks", func(t *testing.T) {
			e := newDamastStackEnv(t, false, nil)
			a := e.layer("a", "main", "Layer A")
			b := e.layer("b", "a", "Layer B")
			require.NoError(t, git_model.UpdateProtectBranch(t.Context(), e.repo, &git_model.ProtectedBranch{
				RepoID: e.repo.ID, RuleName: "main", CanPush: true, EnableStatusCheck: true, StatusCheckContexts: []string{"ci"},
			}, git_model.WhitelistOptions{}))
			for br, state := range map[string]api.CommitStatusState{"a": api.CommitStatusSuccess, "b": api.CommitStatusPending} {
				sha, err := e.headOf(br)
				require.NoError(t, err)
				require.NoError(t, commitstatus.CreateCommitStatus(t.Context(), e.repo, e.owner, sha, &git_model.CommitStatus{State: state, Context: "ci"}))
			}
			res := e.action(b, "merge", e.token, http.StatusConflict, nil)
			assert.Equal(t, []int64{a.Index}, res.Done)
			assert.Equal(t, b.Index, res.FailedAt)
			assert.False(t, e.pr(b).HasMerged)
			assert.Equal(t, "main", e.pr(b).BaseBranch)
		})
	})
}
