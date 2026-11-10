// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package damast

import (
	"context"
	"io"

	git_model "forgejo.org/models/git"
	issues_model "forgejo.org/models/issues"
	access_model "forgejo.org/models/perm/access"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/damast/suggestion"
	"forgejo.org/modules/git"
	"forgejo.org/modules/gitrepo"
)

// CanApplySuggestions reports whether doer may commit review suggestions to
// the head branch of an open pull request: write access to the head branch
// (own repository, or a fork that allows maintainer edits), and push rights if
// the head branch is protected.
func CanApplySuggestions(ctx context.Context, doer *user_model.User, pr *issues_model.PullRequest) (bool, error) {
	if doer == nil || pr.HasMerged || pr.Flow == issues_model.PullRequestFlowAGit {
		return false, nil
	}
	if err := pr.LoadIssue(ctx); err != nil {
		return false, err
	}
	if pr.Issue.IsClosed {
		return false, nil
	}
	if err := pr.LoadHeadRepo(ctx); err != nil {
		return false, err
	}
	if pr.HeadRepo == nil || pr.HeadRepo.IsArchived || !pr.HeadRepo.CanEnableEditor() {
		return false, nil
	}
	perm, err := access_model.GetUserRepoPermission(ctx, pr.HeadRepo, doer)
	if err != nil {
		return false, err
	}
	if !issues_model.CanMaintainerWriteToBranch(ctx, perm, pr.HeadBranch, doer, access_model.GetUserRepoPermission) {
		return false, nil
	}
	pb, err := git_model.GetFirstMatchProtectedBranchRule(ctx, pr.HeadRepoID, pr.HeadBranch)
	if err != nil {
		return false, err
	}
	if pb != nil {
		pb.Repo = pr.HeadRepo
		return pb.CanUserPush(ctx, doer), nil
	}
	return true, nil
}

// Suggestion is the ```suggestion block of a review comment.
type Suggestion struct {
	Comment *issues_model.Comment
	Old     []string // the commented lines at Comment.CommitSHA
	New     []string // the suggested lines, empty to delete
	// Problem is the locale key explaining why the suggestion can't be applied, "" if it can.
	Problem string
}

// ReadFileAt returns the content of path at commitID and its tree entry.
func ReadFileAt(gitRepo *git.Repository, commitID, path string) ([]byte, *git.TreeEntry, error) {
	commit, err := gitRepo.GetCommit(commitID)
	if err != nil {
		return nil, nil, err
	}
	entry, err := commit.GetTreeEntryByPath(path)
	if err != nil {
		return nil, nil, err
	}
	r, err := entry.Blob().DataAsync()
	if err != nil {
		return nil, nil, err
	}
	defer r.Close()
	content, err := io.ReadAll(r)
	return content, entry, err
}

// LoadSuggestion returns the suggestion of a code comment, or nil if it has none.
func LoadSuggestion(ctx context.Context, c *issues_model.Comment) (*Suggestion, error) {
	if c.Type != issues_model.CommentTypeCode {
		return nil, nil
	}
	lines, found, err := suggestion.Parse(c.Content)
	if !found {
		return nil, nil
	}
	s := &Suggestion{Comment: c, New: lines}
	switch {
	case err != nil:
		s.Problem = "damast.suggestions.multiple"
		return s, nil
	case c.Line <= 0:
		s.Problem = "damast.suggestions.old_side"
		return s, nil
	}
	if err := c.LoadIssue(ctx); err != nil {
		return nil, err
	}
	if err := c.Issue.LoadRepo(ctx); err != nil {
		return nil, err
	}
	gitRepo, closer, err := gitrepo.RepositoryFromContextOrOpen(ctx, c.Issue.Repo)
	if err != nil {
		return nil, err
	}
	defer closer.Close()
	content, _, err := ReadFileAt(gitRepo, c.CommitSHA, c.TreePath)
	if err == nil {
		s.Old, err = suggestion.Lines(content, int(c.Line), int(c.ExtraLinesCount)+1)
	}
	if err != nil || c.Invalidated {
		s.Problem = "damast.suggestions.outdated"
	}
	return s, nil
}
