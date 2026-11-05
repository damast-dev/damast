// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package structs

// DamastStack is the stack of pull requests a pull request belongs to (Damast).
// A pull request sits on top of another when its base branch is that one's head branch.
type DamastStack struct {
	// base branch of the bottom layer
	Trunk string `json:"trunk"`
	// number of the pull request that was asked for
	Current int64 `json:"current"`
	// layers in pre-order: every parent comes before its children
	Layers []*DamastStackLayer `json:"layers"`
}

// DamastStackLayer is one pull request in a stack
type DamastStackLayer struct {
	Number  int64  `json:"number"`
	Title   string `json:"title"`
	HTMLURL string `json:"html_url"`
	// open, closed or merged
	State string `json:"state"`
	// title starts with a work-in-progress prefix
	Draft   bool   `json:"draft"`
	Base    string `json:"base"`
	Head    string `json:"head"`
	HeadSHA string `json:"head_sha"`
	// number of the layer below, 0 for the bottom layer
	Parent int64 `json:"parent"`
	Depth  int   `json:"depth"`
	// combined commit status of the head commit, empty without statuses
	Checks CommitStatusState `json:"checks"`
}

// DamastStackActionOption options for rebasing or merging a stack
type DamastStackActionOption struct {
	// head commit of every affected layer, by pull request number, as last seen by the caller;
	// the action stops at a layer whose branch has moved
	// required: true
	Heads map[string]string `json:"heads" binding:"Required"`
	// merge style for "merge up to here"; defaults to the repository's default merge style
	// enum: merge,rebase,rebase-merge,squash,fast-forward-only
	Style string `json:"style"`
}

// DamastStackActionResult is the outcome of a stack rebase or merge
type DamastStackActionResult struct {
	// numbers of the layers that were rebased or merged, in order
	Done []int64 `json:"done"`
	// number of the layer that stopped the action, 0 on success
	FailedAt int64 `json:"failed_at"`
	// why it stopped
	Message string `json:"message,omitempty"`
}
