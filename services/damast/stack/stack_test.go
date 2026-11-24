// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package stack

import (
	"fmt"
	"testing"

	"forgejo.org/models/db"
	git_model "forgejo.org/models/git"
	issues_model "forgejo.org/models/issues"
	"forgejo.org/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	unittest.MainTest(m)
}

var nextIndex int64 = 1000

func addPR(t *testing.T, head, base string, headRepoID int64, closed bool) *issues_model.PullRequest {
	t.Helper()
	nextIndex++
	issue := &issues_model.Issue{RepoID: 1, Index: nextIndex, PosterID: 2, Title: head, IsPull: true, IsClosed: closed}
	require.NoError(t, db.Insert(t.Context(), issue))
	pr := &issues_model.PullRequest{
		IssueID: issue.ID, Index: issue.Index, BaseRepoID: 1, HeadRepoID: headRepoID,
		HeadBranch: head, BaseBranch: base, Flow: issues_model.PullRequestFlowGithub,
	}
	require.NoError(t, db.Insert(t.Context(), pr))
	return pr
}

func numbers(s *Stack) (n []string) {
	for _, l := range s.Layers {
		n = append(n, l.PR.HeadBranch)
	}
	return n
}

func TestFind(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	// st-main <- a <- b <- c, a <- d; plus a fork PR, a closed PR and an unrelated PR on top of the same trunk
	a := addPR(t, "st-a", "st-main", 1, false)
	b := addPR(t, "st-b", "st-a", 1, false)
	c := addPR(t, "st-c", "st-b", 1, false)
	d := addPR(t, "st-d", "st-a", 1, false)
	fork := addPR(t, "st-f", "st-a", 11, false)
	closed := addPR(t, "st-e", "st-b", 1, true)
	other := addPR(t, "st-x", "st-main", 1, false)

	t.Run("tree", func(t *testing.T) {
		for _, pr := range []*issues_model.PullRequest{a, b, c, d} {
			s, err := Find(t.Context(), pr)
			require.NoError(t, err)
			assert.Equal(t, "st-main", s.Trunk)
			assert.Equal(t, []string{"st-a", "st-b", "st-c", "st-d"}, numbers(s), "pre-order, children by number")
			assert.Equal(t, a.ID, s.Root.PR.ID)
			assert.Equal(t, pr.ID, s.Current.PR.ID)
			assert.True(t, s.IsStacked())
		}
		s, err := Find(t.Context(), c)
		require.NoError(t, err)
		assert.Equal(t, 2, s.Current.Depth)
		assert.Len(t, s.Root.Children, 2, "a has two children: a tree, not a line")
		var path []int64
		for _, l := range s.Path(s.Current) {
			path = append(path, l.PR.ID)
		}
		assert.Equal(t, []int64{a.ID, b.ID, c.ID}, path)
		assert.NotNil(t, s.Current.PR.Issue.Repo, "layers come with their repository")
	})

	t.Run("ignored", func(t *testing.T) {
		for _, pr := range []*issues_model.PullRequest{fork, closed, other} {
			s, err := Find(t.Context(), pr)
			require.NoError(t, err)
			assert.False(t, s.IsStacked(), pr.HeadBranch)
			assert.Equal(t, pr.BaseBranch, s.Trunk)
		}
	})

	t.Run("cycle", func(t *testing.T) {
		p := addPR(t, "st-p", "st-q", 1, false)
		q := addPR(t, "st-q", "st-p", 1, false)
		s, err := Find(t.Context(), p)
		require.NoError(t, err)
		assert.Len(t, s.Layers, 2)
		assert.True(t, s.Truncated, "a cycle is unsupported")
		assert.Empty(t, s.Trunk, "no trunk was reached")
		s, err = Find(t.Context(), q)
		require.NoError(t, err)
		assert.Len(t, s.Layers, 2)
		assert.True(t, s.Truncated)
	})

	t.Run("default and protected branches are not layers", func(t *testing.T) {
		require.NoError(t, db.Insert(t.Context(), &git_model.ProtectedBranch{RepoID: 1, RuleName: "st-prot"}))
		a := addPR(t, "st-la", "st-main", 1, false)
		// "sync the default branch into a": head is the default branch
		syncDefault := addPR(t, "master", "st-la", 1, false)
		// a PR from a protected branch into a
		syncProt := addPR(t, "st-prot", "st-la", 1, false)
		// a PR on top of a protected branch is the bottom of its own stack
		onProt := addPR(t, "st-lb", "st-prot", 1, false)
		for _, pr := range []*issues_model.PullRequest{a, syncDefault, syncProt, onProt} {
			s, err := Find(t.Context(), pr)
			require.NoError(t, err)
			assert.False(t, s.IsStacked(), pr.HeadBranch)
			assert.False(t, s.Truncated)
			assert.Equal(t, pr.BaseBranch, s.Trunk)
		}
	})

	t.Run("maxLayers caps fan-out", func(t *testing.T) {
		root := addPR(t, "st-fr", "st-main", 1, false)
		var last *issues_model.PullRequest
		for range maxLayers * 2 {
			last = addPR(t, fmt.Sprintf("st-f%d", nextIndex), "st-fr", 1, false)
		}
		for _, pr := range []*issues_model.PullRequest{root, last} {
			s, err := Find(t.Context(), pr)
			require.NoError(t, err)
			assert.Len(t, s.Layers, maxLayers)
			assert.True(t, s.Truncated)
			assert.Equal(t, "st-main", s.Trunk, "the bottom was reached")
			assert.Contains(t, numbers(s), pr.HeadBranch, "the asked-for PR is always shown")
		}
	})

	t.Run("deep chains never report a wrong trunk", func(t *testing.T) {
		var chain []*issues_model.PullRequest
		for i := range maxLayers + 50 {
			base := "st-main"
			if i > 0 {
				base = chain[i-1].HeadBranch
			}
			chain = append(chain, addPR(t, fmt.Sprintf("st-c%d", i), base, 1, false))
		}
		for _, pr := range []*issues_model.PullRequest{chain[maxLayers+1], chain[maxLayers+49]} {
			s, err := Find(t.Context(), pr)
			require.NoError(t, err)
			assert.True(t, s.Truncated)
			assert.Empty(t, s.Trunk, "the bottom was not reached")
			assert.LessOrEqual(t, len(s.Layers), maxLayers)
			assert.Contains(t, numbers(s), pr.HeadBranch)
		}
		s, err := Find(t.Context(), chain[10])
		require.NoError(t, err)
		assert.True(t, s.Truncated, "more than maxLayers on top")
		assert.Equal(t, "st-main", s.Trunk)
		assert.ErrorIs(t, func() error { _, err := Rebase(t.Context(), s, nil, nil); return err }(), ErrTruncated)
	})

	t.Run("several PRs from one branch", func(t *testing.T) {
		// st-y is the head of two open PRs; the lower number is the parent of st-z
		y1 := addPR(t, "st-y", "st-main", 1, false)
		addPR(t, "st-y", "st-release", 1, false)
		z := addPR(t, "st-z", "st-y", 1, false)
		s, err := Find(t.Context(), z)
		require.NoError(t, err)
		assert.Equal(t, y1.ID, s.Root.PR.ID)
		assert.Equal(t, "st-main", s.Trunk)
		assert.Len(t, s.Layers, 2)
	})
}
