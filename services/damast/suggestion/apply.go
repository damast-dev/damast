// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package suggestion commits ```suggestion blocks of review comments to the
// head branch of a pull request.
package suggestion

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"forgejo.org/models"
	damast_model "forgejo.org/models/damast"
	issues_model "forgejo.org/models/issues"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/damast/coauthor"
	"forgejo.org/modules/damast/suggestion"
	"forgejo.org/modules/gitrepo"
	"forgejo.org/modules/log"
	files_service "forgejo.org/services/repository/files"
)

// Errors returned by Apply. Each has a locale key "damast.suggestions.err_<name>".
var (
	ErrNotAllowed = errors.New("not_allowed")
	ErrClosed     = errors.New("closed")
	ErrInvalid    = errors.New("invalid")
	ErrOutdated   = errors.New("outdated")
	ErrOverlap    = errors.New("overlap")
)

var errStatus = map[error]int{
	ErrNotAllowed: http.StatusForbidden,
	ErrClosed:     http.StatusConflict,
	ErrInvalid:    http.StatusUnprocessableEntity,
	ErrOutdated:   http.StatusConflict,
	ErrOverlap:    http.StatusConflict,
}

// ErrorKey returns the locale key and HTTP status for an error of Apply, or "" for an internal error.
func ErrorKey(err error) (key string, status int) {
	for e, status := range errStatus {
		if errors.Is(err, e) {
			return "damast.suggestions.err_" + e.Error(), status
		}
	}
	return "", http.StatusInternalServerError
}

// Apply commits the suggestions of the given review comments to the head
// branch of pr as one commit, credits their authors with Co-authored-by
// trailers and resolves their conversations. It returns the new commit ID.
func Apply(ctx context.Context, doer *user_model.User, pr *issues_model.PullRequest, commentIDs []int64, message string) (string, error) {
	if err := pr.LoadIssue(ctx); err != nil {
		return "", err
	}
	if pr.HasMerged || pr.Issue.IsClosed {
		return "", ErrClosed
	}
	ok, err := damast_model.CanApplySuggestions(ctx, doer, pr)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", ErrNotAllowed
	}
	slices.Sort(commentIDs)
	commentIDs = slices.Compact(commentIDs)
	if len(commentIDs) == 0 {
		return "", fmt.Errorf("%w: no comments", ErrInvalid)
	}

	sugs := make([]*damast_model.Suggestion, 0, len(commentIDs))
	for _, id := range commentIDs {
		c, err := issues_model.GetCommentByID(ctx, id)
		if issues_model.IsErrCommentNotExist(err) {
			return "", fmt.Errorf("%w: comment %d not found", ErrInvalid, id)
		} else if err != nil {
			return "", err
		}
		if c.IssueID != pr.IssueID {
			return "", fmt.Errorf("%w: comment %d not found", ErrInvalid, id)
		}
		if err := c.LoadReview(ctx); err != nil && !issues_model.IsErrReviewNotExist(err) {
			return "", err
		}
		if c.Review == nil || c.Review.Type == issues_model.ReviewTypePending {
			return "", fmt.Errorf("%w: comment %d is not part of a submitted review", ErrInvalid, id)
		}
		s, err := damast_model.LoadSuggestion(ctx, c)
		if err != nil {
			return "", err
		}
		if s == nil {
			return "", fmt.Errorf("%w: comment %d has no suggestion", ErrInvalid, id)
		}
		switch s.Problem {
		case "":
		case "damast.suggestions.outdated":
			return "", fmt.Errorf("%w: comment %d", ErrOutdated, id)
		default:
			return "", fmt.Errorf("%w: comment %d: %s", ErrInvalid, id, s.Problem)
		}
		sugs = append(sugs, s)
	}

	headRepo, err := gitrepo.OpenRepository(ctx, pr.HeadRepo)
	if err != nil {
		return "", err
	}
	defer headRepo.Close()
	headID, err := headRepo.GetBranchCommitID(pr.HeadBranch)
	if err != nil {
		return "", err
	}

	type file struct {
		content []byte
		sha     string
		opts    *files_service.RepoFileOptions
		edits   []suggestion.Edit
	}
	files := map[string]*file{}
	var paths []string
	for _, s := range sugs {
		c := s.Comment
		// Follow the commented lines to the current head, like the diff view does.
		blame, err := headRepo.ReverseLineBlame(c.CommitSHA, c.TreePath, uint64(c.Line), headID)
		if err != nil || blame.CommitID != headID {
			return "", fmt.Errorf("%w: comment %d", ErrOutdated, c.ID)
		}
		f := files[blame.FilePath]
		if f == nil {
			content, entry, err := damast_model.ReadFileAt(headRepo, headID, blame.FilePath)
			if err != nil {
				return "", fmt.Errorf("%w: comment %d", ErrOutdated, c.ID)
			}
			f = &file{content: content, sha: entry.ID.String(), opts: files_service.RepoFileOptionMode(entry.Mode())}
			files[blame.FilePath] = f
			paths = append(paths, blame.FilePath)
		}
		count := int(c.ExtraLinesCount) + 1
		current, err := suggestion.Lines(f.content, int(blame.LineNumber), count)
		if err != nil || !slices.Equal(current, s.Old) {
			return "", fmt.Errorf("%w: comment %d", ErrOutdated, c.ID)
		}
		f.edits = append(f.edits, suggestion.Edit{Start: int(blame.LineNumber), Count: count, Lines: s.New})
	}

	changes := make([]*files_service.ChangeRepoFile, 0, len(paths))
	for _, p := range paths {
		f := files[p]
		content, err := suggestion.Replace(f.content, f.edits)
		if errors.Is(err, suggestion.ErrOverlap) {
			return "", ErrOverlap
		} else if err != nil {
			return "", err
		}
		changes = append(changes, &files_service.ChangeRepoFile{
			Operation:     "update",
			TreePath:      p,
			ContentReader: bytes.NewReader(content),
			SHA:           f.sha,
			Options:       f.opts,
		})
	}

	res, err := files_service.ChangeRepoFiles(ctx, pr.HeadRepo, doer, &files_service.ChangeRepoFilesOptions{
		LastCommitID: headID,
		OldBranch:    pr.HeadBranch,
		NewBranch:    pr.HeadBranch,
		Message:      withCoAuthors(ctx, message, doer, sugs),
		Files:        changes,
	})
	switch {
	case models.IsErrUserCannotCommit(err), models.IsErrFilePathProtected(err):
		return "", fmt.Errorf("%w: %v", ErrNotAllowed, err)
	case models.IsErrCommitIDDoesNotMatch(err), models.IsErrSHADoesNotMatch(err), models.IsErrSHAOrCommitIDNotProvided(err):
		return "", fmt.Errorf("%w: %v", ErrOutdated, err)
	case err != nil:
		return "", err
	}

	for _, s := range sugs {
		if err := resolveConversation(ctx, s.Comment, doer); err != nil {
			log.Error("resolve conversation of comment %d: %v", s.Comment.ID, err)
		}
	}
	return res.Commit.SHA, nil
}

var trailerLine = regexp.MustCompile(`^[A-Za-z0-9-]+: `)

// withCoAuthors appends a Co-authored-by trailer for every suggestion author
// except doer and those already credited, using the email Forgejo would put in
// their commits (the noreply address if they keep their email private).
func withCoAuthors(ctx context.Context, message string, doer *user_model.User, sugs []*damast_model.Suggestion) string {
	message = strings.TrimSpace(strings.ReplaceAll(message, "\r\n", "\n"))
	if message == "" {
		message = suggestion.DefaultMessage
	}
	seen := map[string]bool{strings.ToLower(doer.GetEmail()): true}
	for _, id := range coauthor.Parse(message, "") {
		seen[strings.ToLower(id.Email)] = true
	}
	var trailers []string
	for _, s := range sugs {
		if err := s.Comment.LoadPoster(ctx); err != nil || s.Comment.Poster == nil || s.Comment.Poster.ID <= 0 {
			continue // ghost or migrated author: no account to credit
		}
		u := s.Comment.Poster
		email := u.GetEmail()
		if seen[strings.ToLower(email)] {
			continue
		}
		seen[strings.ToLower(email)] = true
		trailers = append(trailers, fmt.Sprintf("Co-authored-by: %s <%s>", u.GitName(), email))
	}
	if len(trailers) == 0 {
		return message
	}
	sep := "\n\n"
	paragraphs := strings.Split(message, "\n\n")
	if last := paragraphs[len(paragraphs)-1]; len(paragraphs) > 1 && allMatch(strings.Split(last, "\n"), trailerLine) {
		sep = "\n" // extend the existing trailer block
	}
	return message + sep + strings.Join(trailers, "\n")
}

func allMatch(lines []string, re *regexp.Regexp) bool {
	for _, l := range lines {
		if !re.MatchString(l) {
			return false
		}
	}
	return true
}

// resolveConversation resolves the conversation c belongs to. Forgejo keeps
// the resolved state on the first comment of a conversation.
func resolveConversation(ctx context.Context, c *issues_model.Comment, doer *user_model.User) error {
	conv, err := issues_model.FindComments(ctx, &issues_model.FindCommentsOptions{
		Type:     issues_model.CommentTypeCode,
		IssueID:  c.IssueID,
		ReviewID: c.ReviewID,
		TreePath: c.TreePath,
		Line:     c.Line,
	})
	if err != nil || len(conv) == 0 {
		return err
	}
	return issues_model.MarkConversation(ctx, conv[0], doer, true)
}
