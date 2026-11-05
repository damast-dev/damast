// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"errors"
	"net/http"
	"strings"

	damast_model "forgejo.org/models/damast"
	issues_model "forgejo.org/models/issues"
	"forgejo.org/modules/translation"
	"forgejo.org/modules/web"
	"forgejo.org/services/context"
	suggestion_service "forgejo.org/services/damast/suggestion"
)

// DamastSuggestion is a review suggestion in the Damast API.
type DamastSuggestion struct {
	CommentID int64 `json:"comment_id"`
	// ContentVersion counts edits of the comment; pass it back in content_versions to refuse edited suggestions.
	ContentVersion int      `json:"content_version"`
	ReviewID       int64    `json:"review_id"`
	Author         string   `json:"author"`
	Path           string   `json:"path"`
	StartLine      int64    `json:"start_line"`
	EndLine        int64    `json:"end_line"`
	Old            []string `json:"old_lines"`
	New            []string `json:"new_lines"`
	Resolved       bool     `json:"resolved"`
	Applicable     bool     `json:"applicable"`
	// Problem is why it can't be applied: "multiple", "old_side" or "outdated"; empty otherwise.
	Problem string `json:"problem,omitempty"`
	HTMLURL string `json:"html_url"`
}

// DamastApplySuggestionsOption selects the suggestions to commit.
type DamastApplySuggestionsOption struct {
	CommentIDs []int64 `json:"comment_ids" binding:"Required"`
	Message    string  `json:"message"`
	// ContentVersions maps comment IDs to the content_version the client has shown; edited comments are refused.
	ContentVersions map[int64]int `json:"content_versions"`
}

// DamastApplySuggestionsResult is the commit that applied the suggestions.
type DamastApplySuggestionsResult struct {
	Commit string `json:"commit"`
}

func damastPullRequest(ctx *context.APIContext) *issues_model.PullRequest {
	pr, err := issues_model.GetPullRequestByIndex(ctx, ctx.Repo().Repository.ID, ctx.ParamsInt64(":index"))
	if issues_model.IsErrPullRequestNotExist(err) {
		ctx.NotFound()
		return nil
	} else if err != nil {
		ctx.Error(http.StatusInternalServerError, "GetPullRequestByIndex", err)
		return nil
	}
	return pr
}

// DamastListSuggestions lists the review suggestions of a pull request.
// GET /repos/{owner}/{repo}/pulls/{index}/damast/suggestions
func DamastListSuggestions(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/pulls/{index}/damast/suggestions repository repoDamastListSuggestions
	// ---
	// summary: List the suggested changes in a pull request's submitted reviews (Damast)
	// produces:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repo
	//   type: string
	//   required: true
	// - name: index
	//   in: path
	//   description: index of the pull request
	//   type: integer
	//   format: int64
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/DamastSuggestionList"
	//   "404":
	//     "$ref": "#/responses/notFound"
	pr := damastPullRequest(ctx)
	if pr == nil {
		return
	}
	if err := pr.LoadIssue(ctx); err != nil {
		ctx.Error(http.StatusInternalServerError, "LoadIssue", err)
		return
	}
	comments, err := issues_model.FindComments(ctx, &issues_model.FindCommentsOptions{Type: issues_model.CommentTypeCode, IssueID: pr.IssueID})
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "FindComments", err)
		return
	}
	if err := comments.LoadPosters(ctx); err != nil {
		ctx.Error(http.StatusInternalServerError, "LoadPosters", err)
		return
	}
	canApply, err := damast_model.CanApplySuggestions(ctx, ctx.Doer(), pr)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "CanApplySuggestions", err)
		return
	}
	out := []DamastSuggestion{}
	for _, c := range comments {
		if err := c.LoadReview(ctx); err != nil && !issues_model.IsErrReviewNotExist(err) {
			ctx.Error(http.StatusInternalServerError, "LoadReview", err)
			return
		}
		if c.Review == nil || c.Review.Type == issues_model.ReviewTypePending {
			continue // pending reviews are private to their author and can't be applied
		}
		s, err := damast_model.LoadSuggestion(ctx, c)
		if err != nil {
			ctx.Error(http.StatusInternalServerError, "LoadSuggestion", err)
			return
		}
		if s == nil {
			continue
		}
		c.Issue = pr.Issue
		out = append(out, DamastSuggestion{
			CommentID:      c.ID,
			ContentVersion: c.ContentVersion,
			ReviewID:       c.ReviewID,
			Author:         c.Poster.Name,
			Path:           c.TreePath,
			StartLine:      int64(c.UnsignedLine()),
			EndLine:        int64(c.UnsignedDisplayLine()),
			Old:            s.Old,
			New:            s.New,
			Resolved:       c.IsResolved(),
			Applicable:     canApply && s.Problem == "",
			Problem:        strings.TrimPrefix(s.Problem, "damast.suggestions."),
			HTMLURL:        c.HTMLURL(ctx),
		})
	}
	ctx.JSON(http.StatusOK, out)
}

// DamastApplySuggestions commits review suggestions to the head branch as one commit.
// POST /repos/{owner}/{repo}/pulls/{index}/damast/suggestions/apply
func DamastApplySuggestions(ctx *context.APIContext) {
	// swagger:operation POST /repos/{owner}/{repo}/pulls/{index}/damast/suggestions/apply repository repoDamastApplySuggestions
	// ---
	// summary: Commit suggested changes to the pull request's head branch as one commit (Damast)
	// produces:
	// - application/json
	// consumes:
	// - application/json
	// parameters:
	// - name: owner
	//   in: path
	//   description: owner of the repo
	//   type: string
	//   required: true
	// - name: repo
	//   in: path
	//   description: name of the repo
	//   type: string
	//   required: true
	// - name: index
	//   in: path
	//   description: index of the pull request
	//   type: integer
	//   format: int64
	//   required: true
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/DamastApplySuggestionsOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/DamastApplySuggestionsResult"
	//   "403":
	//     "$ref": "#/responses/forbidden"
	//   "404":
	//     "$ref": "#/responses/notFound"
	//   "409":
	//     "$ref": "#/responses/error"
	//   "422":
	//     "$ref": "#/responses/validationError"
	pr := damastPullRequest(ctx)
	if pr == nil {
		return
	}
	form := web.GetForm(ctx).(*DamastApplySuggestionsOption)
	sha, err := suggestion_service.Apply(ctx, ctx.Doer(), pr, form.CommentIDs, form.ContentVersions, form.Message)
	if err != nil {
		key, status := suggestion_service.ErrorKey(err)
		if key == "" {
			ctx.Error(http.StatusInternalServerError, "Apply", err)
			return
		}
		ctx.Error(status, "", errors.New(translation.NewLocale("en-US").TrString(key)+" ("+err.Error()+")"))
		return
	}
	ctx.JSON(http.StatusOK, DamastApplySuggestionsResult{Commit: sha})
}
