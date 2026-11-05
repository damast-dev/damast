// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package stack

import (
	"testing"

	"forgejo.org/models/db"
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
		s, err = Find(t.Context(), q)
		require.NoError(t, err)
		assert.Len(t, s.Layers, 2)
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
