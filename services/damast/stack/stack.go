// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package stack derives stacked pull requests from the base and head branches of
// open pull requests in one repository. A PR whose base branch is the head branch
// of another open same-repo PR sits on top of it. Nothing is stored: the stack is
// rebuilt from the pull_request table on every request, so going back to Forgejo
// leaves no trace. A PR whose head branch is the default branch or a protected branch
// is never a layer: such branches end the stack like a trunk.
package stack

import (
	"cmp"
	"context"
	"slices"

	"forgejo.org/models/db"
	git_model "forgejo.org/models/git"
	issues_model "forgejo.org/models/issues"
)

// maxLayers caps the number of pull requests in one stack, fan-out included; real stacks
// are a handful of layers. A bigger or circular stack is Truncated.
const maxLayers = 100

// Layer is one pull request in a stack.
type Layer struct {
	PR       *issues_model.PullRequest
	Parent   *Layer
	Children []*Layer
	Depth    int
}

// Stack is a tree of layers. Root is the bottom layer, the one whose base branch
// (Trunk) is not the head of another open PR in the stack.
type Stack struct {
	// Trunk is the base branch of the bottom layer; empty when the walk down never reached it.
	Trunk   string
	Root    *Layer
	Layers  []*Layer // pre-order: every parent comes before its children
	Current *Layer
	// Truncated means the stack has more than maxLayers pull requests or is circular. The layers
	// found are shown, but rebase and merge are refused.
	Truncated bool
}

// IsStacked reports whether the stack has more than the current PR.
func (s *Stack) IsStacked() bool {
	return len(s.Layers) > 1
}

// Path returns the layers from the bottom up to and including l.
func (s *Stack) Path(l *Layer) []*Layer {
	var path []*Layer
	for ; l != nil; l = l.Parent {
		path = append([]*Layer{l}, path...)
	}
	return path
}

// openSameRepo returns up to limit open, unmerged, branch-based PRs whose head and base are both in repoID
// and whose column col (head_branch or base_branch) equals branch, lowest index first.
// Fork PRs and AGit PRs are never layers: Damast cannot rebase or retarget them safely.
func openSameRepo(ctx context.Context, repoID int64, col, branch string, limit int) ([]*issues_model.PullRequest, error) {
	prs := make([]*issues_model.PullRequest, 0, 2)
	return prs, db.GetEngine(ctx).
		Join("INNER", "issue", "issue.id = pull_request.issue_id").
		Where("pull_request.base_repo_id = ? AND pull_request.head_repo_id = ?", repoID, repoID).
		And("pull_request.has_merged = ? AND issue.is_closed = ?", false, false).
		And("pull_request.flow = ?", issues_model.PullRequestFlowGithub).
		And("pull_request."+col+" = ?", branch).
		OrderBy("pull_request.`index`").
		Limit(limit).
		Find(&prs)
}

// Find builds the stack that pr belongs to. All layers are open. For a closed, merged,
// fork or AGit PR, or one whose head branch is the default or a protected branch, the stack is just pr.
func Find(ctx context.Context, pr *issues_model.PullRequest) (*Stack, error) {
	current := &Layer{PR: pr}
	s := &Stack{Trunk: pr.BaseBranch, Root: current, Layers: []*Layer{current}, Current: current}
	if err := pr.LoadIssue(ctx); err != nil {
		return nil, err
	}
	if pr.HasMerged || pr.Issue.IsClosed || pr.HeadRepoID != pr.BaseRepoID || pr.Flow != issues_model.PullRequestFlowGithub {
		return s, nil
	}
	if err := pr.LoadBaseRepo(ctx); err != nil {
		return nil, err
	}
	repoID := pr.BaseRepoID
	rules, err := git_model.FindRepoProtectedBranchRules(ctx, repoID)
	if err != nil {
		return nil, err
	}
	longLived := func(branch string) bool {
		return branch == pr.BaseRepo.DefaultBranch || rules.GetFirstMatched(branch) != nil
	}
	if longLived(pr.HeadBranch) {
		return s, nil
	}

	// Walk down to the bottom layer. A branch can be the head of several open PRs;
	// shortcut: the lowest index wins, the others are not part of this stack.
	chain := []*issues_model.PullRequest{pr} // pr first, bottom last
	seen := map[int64]bool{pr.ID: true}
	reachedTrunk := true
	for bottom := pr; !longLived(bottom.BaseBranch); {
		parents, err := openSameRepo(ctx, repoID, "head_branch", bottom.BaseBranch, maxLayers)
		if err != nil {
			return nil, err
		}
		var parent *issues_model.PullRequest
		for _, p := range parents {
			if !seen[p.ID] {
				parent = p
				break
			}
		}
		if parent == nil {
			reachedTrunk = len(parents) == 0 // otherwise every candidate is already below us: a cycle
			break
		}
		if len(seen) >= maxLayers {
			reachedTrunk = false
			break
		}
		seen[parent.ID] = true
		chain = append(chain, parent)
		bottom = parent
	}
	bottom := chain[len(chain)-1]
	s.Truncated = !reachedTrunk
	s.Trunk = ""
	if reachedTrunk {
		s.Trunk = bottom.BaseBranch
	}

	// The path from the bottom up to pr, then everything on top of it breadth first.
	root := current
	if bottom.ID != pr.ID {
		root = &Layer{PR: bottom}
	}
	s.Root = root
	queue := []*Layer{root}
	for i := len(chain) - 2; i >= 0; i-- {
		l := current
		if i > 0 {
			l = &Layer{PR: chain[i]}
		}
		parent := queue[len(queue)-1]
		l.Parent, l.Depth = parent, parent.Depth+1
		parent.Children = append(parent.Children, l)
		queue = append(queue, l)
	}
bfs:
	for i := 0; i < len(queue); i++ {
		l := queue[i]
		children, err := openSameRepo(ctx, repoID, "base_branch", l.PR.HeadBranch, maxLayers)
		if err != nil {
			return nil, err
		}
		for _, c := range children {
			if seen[c.ID] {
				for a := l; a != nil; a = a.Parent {
					if a.PR.ID == c.ID {
						s.Truncated = true // the PR sits below itself
					}
				}
				continue
			}
			if longLived(c.HeadBranch) {
				continue
			}
			if len(seen) >= maxLayers {
				s.Truncated = true
				break bfs
			}
			seen[c.ID] = true
			child := &Layer{PR: c, Parent: l, Depth: l.Depth + 1}
			l.Children = append(l.Children, child)
			queue = append(queue, child)
		}
	}

	s.Layers = s.Layers[:0]
	var walk func(*Layer)
	walk = func(l *Layer) {
		s.Layers = append(s.Layers, l)
		slices.SortFunc(l.Children, func(a, b *Layer) int { return cmp.Compare(a.PR.Index, b.PR.Index) }) // the path down to pr was attached first
		for _, c := range l.Children {
			walk(c)
		}
	}
	walk(root)

	prs := make(issues_model.PullRequestList, 0, len(s.Layers))
	for _, l := range s.Layers {
		prs = append(prs, l.PR)
	}
	if err := prs.LoadAttributes(ctx); err != nil {
		return nil, err
	}
	for _, p := range prs { // all layers live in pr's repository
		p.BaseRepo, p.HeadRepo, p.Issue.Repo = pr.BaseRepo, pr.BaseRepo, pr.BaseRepo
	}
	return s, nil
}
