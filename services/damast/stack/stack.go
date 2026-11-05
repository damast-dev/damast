// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package stack derives stacked pull requests from the base and head branches of
// open pull requests in one repository. A PR whose base branch is the head branch
// of another open same-repo PR sits on top of it. Nothing is stored: the stack is
// rebuilt from the pull_request table on every request, so going back to Forgejo
// leaves no trace.
package stack

import (
	"context"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
)

// maxLayers bounds the walk; real stacks are a handful of layers deep.
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
	Trunk   string
	Root    *Layer
	Layers  []*Layer // pre-order: every parent comes before its children
	Current *Layer
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

// openSameRepo returns open, unmerged, branch-based PRs whose head and base are both in repoID
// and whose column col (head_branch or base_branch) equals branch, lowest index first.
// Fork PRs and AGit PRs are never layers: Damast cannot rebase or retarget them safely.
func openSameRepo(ctx context.Context, repoID int64, col, branch string) ([]*issues_model.PullRequest, error) {
	prs := make([]*issues_model.PullRequest, 0, 2)
	return prs, db.GetEngine(ctx).
		Join("INNER", "issue", "issue.id = pull_request.issue_id").
		Where("pull_request.base_repo_id = ? AND pull_request.head_repo_id = ?", repoID, repoID).
		And("pull_request.has_merged = ? AND issue.is_closed = ?", false, false).
		And("pull_request.flow = ?", issues_model.PullRequestFlowGithub).
		And("pull_request."+col+" = ?", branch).
		OrderBy("pull_request.`index`").
		Find(&prs)
}

// Find builds the stack that pr belongs to. All layers are open. For a closed, merged,
// fork or AGit PR the stack is just pr.
func Find(ctx context.Context, pr *issues_model.PullRequest) (*Stack, error) {
	current := &Layer{PR: pr}
	s := &Stack{Trunk: pr.BaseBranch, Root: current, Layers: []*Layer{current}, Current: current}
	if err := pr.LoadIssue(ctx); err != nil {
		return nil, err
	}
	if pr.HasMerged || pr.Issue.IsClosed || pr.HeadRepoID != pr.BaseRepoID || pr.Flow != issues_model.PullRequestFlowGithub {
		return s, nil
	}
	repoID := pr.BaseRepoID
	seen := map[int64]bool{pr.ID: true}

	// Walk down to the bottom layer. A branch can be the head of several open PRs;
	// shortcut: the lowest index wins, the others are not part of this stack.
	bottom := pr
	for len(seen) < maxLayers {
		parents, err := openSameRepo(ctx, repoID, "head_branch", bottom.BaseBranch)
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
			break
		}
		seen[parent.ID] = true
		bottom = parent
	}

	// Build the tree upwards from the bottom layer, breadth first.
	root := current
	if bottom.ID != pr.ID {
		root = &Layer{PR: bottom}
	}
	s.Root, s.Trunk = root, bottom.BaseBranch
	seen = map[int64]bool{bottom.ID: true}
	queue := []*Layer{root}
	for i := 0; i < len(queue) && len(seen) < maxLayers; i++ {
		l := queue[i]
		children, err := openSameRepo(ctx, repoID, "base_branch", l.PR.HeadBranch)
		if err != nil {
			return nil, err
		}
		for _, c := range children {
			if seen[c.ID] {
				continue
			}
			seen[c.ID] = true
			child := current
			if c.ID != pr.ID {
				child = &Layer{PR: c}
			}
			child.Parent, child.Depth = l, l.Depth+1
			l.Children = append(l.Children, child)
			queue = append(queue, child)
		}
	}
	if current != root && current.Parent == nil {
		// only possible when maxLayers cut the walk short
		return &Stack{Trunk: pr.BaseBranch, Root: current, Layers: []*Layer{current}, Current: current}, nil
	}

	s.Layers = s.Layers[:0]
	var walk func(*Layer)
	walk = func(l *Layer) {
		s.Layers = append(s.Layers, l)
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
	if err := pr.LoadBaseRepo(ctx); err != nil {
		return nil, err
	}
	for _, p := range prs { // all layers live in pr's repository
		p.BaseRepo, p.HeadRepo, p.Issue.Repo = pr.BaseRepo, pr.BaseRepo, pr.BaseRepo
	}
	return s, nil
}
