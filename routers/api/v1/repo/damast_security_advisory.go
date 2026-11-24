// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"forgejo.org/models/damast"
	access_model "forgejo.org/models/perm/access"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/log"
	"forgejo.org/modules/util"
	"forgejo.org/modules/web"
	"forgejo.org/services/context"
	"forgejo.org/services/convert"
	"forgejo.org/services/damast/security"
)

// REST API for repository security advisories, shaped like GitHub's
// (https://docs.github.com/en/rest/security-advisories/repository-advisories). Every denial is a 404, also for
// anonymous users and for write requests, so the API never tells an unpublished advisory from a missing one.

func damastAPIError(ctx *context.APIContext, err error) {
	var inputErr *security.InputError
	switch {
	case errors.Is(err, security.ErrTooManyReports):
		ctx.Error(http.StatusTooManyRequests, "", err.Error())
	case errors.As(err, &inputErr):
		ctx.Error(http.StatusUnprocessableEntity, "", err.Error())
	default:
		ctx.InternalServerError(err)
	}
}

// damastAccess is the doer and permission every advisory check uses. A public-only token gets what an anonymous
// visitor gets: published advisories only, never drafts, reports or discussions (as GitHub's public_repo scope).
// checkTokenPublicOnly has already turned such tokens away from private repositories.
func damastAccess(ctx *context.APIContext) (*user_model.User, *access_model.Permission) {
	if !ctx.PublicOnly() {
		return ctx.Doer(), ctx.Permission()
	}
	if p, ok := ctx.Data["DamastPublicOnlyPerm"].(*access_model.Permission); ok {
		return nil, p
	}
	p, err := access_model.GetUserRepoPermission(ctx, ctx.Repo().Repository, nil)
	if err != nil {
		log.Error("GetUserRepoPermission: %v", err)
		p = access_model.Permission{} // no access: every check answers 404
	}
	ctx.Data["DamastPublicOnlyPerm"] = &p
	return nil, &p
}

// DamastAdvisoryReqRead lets only users in who may read the repository's code (or admins).
func DamastAdvisoryReqRead(ctx *context.APIContext) {
	if _, p := damastAccess(ctx); !security.CanReadRepo(p) {
		ctx.NotFound()
	}
}

// DamastAdvisoryReqAdmin lets only repository admins in.
func DamastAdvisoryReqAdmin(ctx *context.APIContext) {
	if _, p := damastAccess(ctx); !security.IsAdmin(p) {
		ctx.NotFound()
	}
}

// DamastAdvisoryAssignment loads the advisory and the doer's level, or answers 404.
func DamastAdvisoryAssignment(ctx *context.APIContext) {
	a, err := damast.GetAdvisory(ctx, ctx.Repo().Repository.ID, ctx.Params("ghsa_id"))
	if errors.Is(err, util.ErrNotExist) {
		ctx.NotFound()
		return
	} else if err != nil {
		ctx.InternalServerError(err)
		return
	}
	doer, p := damastAccess(ctx)
	level, err := security.AccessLevel(ctx, ctx.Repo().Repository, a, doer, p)
	if err != nil {
		ctx.InternalServerError(err)
		return
	}
	if level == security.None {
		ctx.NotFound()
		return
	}
	ctx.Data["DamastAdvisory"] = a
	ctx.Data["DamastLevel"] = level
}

// DamastAdvisoryReq requires at least the level for the loaded advisory.
func DamastAdvisoryReq(min security.Level) func(ctx *context.APIContext) {
	return func(ctx *context.APIContext) {
		if level, ok := ctx.Data["DamastLevel"].(security.Level); !ok || level < min {
			ctx.NotFound()
		}
	}
}

func damastAPIAdvisory(ctx *context.APIContext) (*damast.SecurityAdvisory, security.Level) {
	return ctx.Data["DamastAdvisory"].(*damast.SecurityAdvisory), ctx.Data["DamastLevel"].(security.Level)
}

func damastRespond(ctx *context.APIContext, status int, a *damast.SecurityAdvisory, level security.Level) {
	r, err := security.ToAPI(ctx, ctx.Repo().Repository, a, ctx.Doer(), level)
	if err != nil {
		ctx.InternalServerError(err)
		return
	}
	ctx.JSON(status, r)
}

// DamastListAdvisories lists the advisories the doer may see. ?state= filters by state.
func DamastListAdvisories(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/security-advisories repository repoListSecurityAdvisories
	// ---
	// summary: List the repository's security advisories the user may see
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
	// - name: state
	//   in: query
	//   description: only advisories in this state (triage, draft, published, closed, withdrawn)
	//   type: string
	// - name: page
	//   in: query
	//   description: page number of results to return (1-based)
	//   type: integer
	// - name: per_page
	//   in: query
	//   description: page size of results (GitHub's name; limit works too)
	//   type: integer
	// - name: limit
	//   in: query
	//   description: page size of results
	//   type: integer
	// responses:
	//   "200":
	//     "$ref": "#/responses/DamastSecurityAdvisoryList"
	//   "404":
	//     "$ref": "#/responses/notFound"
	//   "422":
	//     "$ref": "#/responses/validationError"
	repo := ctx.Repo().Repository
	doer, p := damastAccess(ctx)
	state := ctx.FormString("state")
	if state != "" {
		if err := security.CheckState(state); err != nil {
			damastAPIError(ctx, err)
			return
		}
	}
	list, err := security.Visible(ctx, repo, doer, p)
	if err != nil {
		ctx.InternalServerError(err)
		return
	}
	if state != "" {
		list = slices.DeleteFunc(list, func(a *damast.SecurityAdvisory) bool { return a.State != state })
	}
	list = damastPage(ctx, list)
	res := make([]*security.APIAdvisory, 0, len(list))
	for _, a := range list {
		level, err := security.AccessLevel(ctx, repo, a, doer, p)
		if err != nil {
			ctx.InternalServerError(err)
			return
		}
		r, err := security.ToAPI(ctx, repo, a, ctx.Doer(), level)
		if err != nil {
			ctx.InternalServerError(err)
			return
		}
		res = append(res, r)
	}
	ctx.JSON(http.StatusOK, res)
}

// damastPage cuts one page out of a list that was loaded in full (see the shortcut in security.Visible) and sets
// X-Total-Count and Link. per_page is GitHub's name for the page size, limit Forgejo's.
func damastPage[T any](ctx *context.APIContext, list []T) []T {
	size := ctx.FormInt("per_page")
	if size == 0 {
		size = ctx.FormInt("limit")
	}
	size = convert.ToCorrectPageSize(size)
	page := min(max(ctx.FormInt("page"), 1), len(list)+1) // clamped first, so the product cannot overflow
	ctx.SetTotalCountHeader(int64(len(list)))
	ctx.SetLinkHeader(len(list), size)
	start := min((page-1)*size, len(list))
	return list[start:min(start+size, len(list))]
}

// DamastReqWritable refuses changes to an archived repository's advisories with 423 Locked, like Forgejo's other
// archived writes. It runs after the access checks, so people without access still get a 404.
func DamastReqWritable(ctx *context.APIContext) {
	if ctx.Repo().Repository.IsArchived {
		ctx.Error(http.StatusLocked, "RepoArchived", fmt.Errorf("%s is archived", ctx.Repo().Repository.LogString()))
	}
}

// DamastCreateAdvisory creates a draft advisory (repository admins).
func DamastCreateAdvisory(ctx *context.APIContext) {
	// swagger:operation POST /repos/{owner}/{repo}/security-advisories repository repoCreateSecurityAdvisory
	// ---
	// summary: Create a draft security advisory (repository admins)
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
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/DamastAdvisoryCreateOption"
	// responses:
	//   "201":
	//     "$ref": "#/responses/DamastSecurityAdvisory"
	//   "404":
	//     "$ref": "#/responses/notFound"
	//   "422":
	//     "$ref": "#/responses/validationError"
	in, err := security.InputFromAPI(ctx, web.GetForm(ctx).(*security.APICreateOption))
	if err != nil {
		damastAPIError(ctx, err)
		return
	}
	a, err := security.Create(ctx, ctx.Repo().Repository, ctx.Doer(), in)
	if err != nil {
		damastAPIError(ctx, err)
		return
	}
	damastRespond(ctx, http.StatusCreated, a, security.Admin)
}

// DamastAdvisoryReqReporter lets only users in who may submit a private vulnerability report.
func DamastAdvisoryReqReporter(ctx *context.APIContext) {
	doer, p := damastAccess(ctx)
	ok, err := security.CanReport(ctx, ctx.Repo().Repository, doer, security.CanReadRepo(p))
	if err != nil {
		ctx.InternalServerError(err)
		return
	}
	if !ok {
		ctx.NotFound()
	}
}

// DamastReportVulnerability submits a private vulnerability report.
func DamastReportVulnerability(ctx *context.APIContext) {
	// swagger:operation POST /repos/{owner}/{repo}/security-advisories/reports repository repoReportVulnerability
	// ---
	// summary: Report a vulnerability privately (needs private vulnerability reporting)
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
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/DamastAdvisoryReportOption"
	// responses:
	//   "201":
	//     "$ref": "#/responses/DamastSecurityAdvisory"
	//   "404":
	//     "$ref": "#/responses/notFound"
	//   "422":
	//     "$ref": "#/responses/validationError"
	//   "429":
	//     "$ref": "#/responses/error"
	repo := ctx.Repo().Repository
	form := web.GetForm(ctx).(*security.APIReportOption)
	in, err := security.InputFromAPI(ctx, &security.APICreateOption{APIReportOption: *form})
	if err != nil {
		damastAPIError(ctx, err)
		return
	}
	a, err := security.Report(ctx, repo, ctx.Doer(), in)
	if err != nil {
		damastAPIError(ctx, err)
		return
	}
	damastRespond(ctx, http.StatusCreated, a, security.Participant)
}

// DamastGetAdvisory returns one advisory.
func DamastGetAdvisory(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/security-advisories/{ghsa_id} repository repoGetSecurityAdvisory
	// ---
	// summary: Get a security advisory
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
	// - name: ghsa_id
	//   in: path
	//   description: id of the advisory (DAMAST-...)
	//   type: string
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/DamastSecurityAdvisory"
	//   "404":
	//     "$ref": "#/responses/notFound"
	a, level := damastAPIAdvisory(ctx)
	damastRespond(ctx, http.StatusOK, a, level)
}

// DamastUpdateAdvisory edits an advisory, changes its state or its collaborators (repository admins).
func DamastUpdateAdvisory(ctx *context.APIContext) {
	// swagger:operation PATCH /repos/{owner}/{repo}/security-advisories/{ghsa_id} repository repoEditSecurityAdvisory
	// ---
	// summary: Edit a security advisory, its collaborators or its state, all or nothing (repository admins)
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
	// - name: ghsa_id
	//   in: path
	//   description: id of the advisory (DAMAST-...)
	//   type: string
	//   required: true
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/DamastAdvisoryUpdateOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/DamastSecurityAdvisory"
	//   "404":
	//     "$ref": "#/responses/notFound"
	//   "422":
	//     "$ref": "#/responses/validationError"
	repo := ctx.Repo().Repository
	a, level := damastAPIAdvisory(ctx)
	form := web.GetForm(ctx).(*security.APIUpdateOption)

	var collaborators []*user_model.User
	if form.CollaboratingUsers != nil {
		collaborators = []*user_model.User{}
		for _, name := range *form.CollaboratingUsers {
			u, err := user_model.GetUserByName(ctx, name)
			if user_model.IsErrUserNotExist(err) {
				damastAPIError(ctx, &security.InputError{Key: "damast.security.error.unknown_user", Args: []any{name}})
				return
			} else if err != nil {
				ctx.InternalServerError(err)
				return
			}
			collaborators = append(collaborators, u)
		}
	}

	o, err := security.OptionFromAdvisory(ctx, a)
	if err != nil {
		ctx.InternalServerError(err)
		return
	}
	changed := false
	set := func(dst, src *string) {
		if src != nil {
			*dst, changed = *src, true
		}
	}
	set(&o.Summary, form.Summary)
	set(&o.Description, form.Description)
	set(&o.Severity, form.Severity)
	set(&o.CVSSVectorString, form.CVSSVectorString)
	if form.CveID.Set { // null clears the CVE, like on GitHub
		o.CveID, changed = "", true
		if form.CveID.Value != nil {
			o.CveID = *form.CveID.Value
		}
	}
	if form.CWEIDs != nil {
		o.CWEIDs, changed = *form.CWEIDs, true
	}
	if form.Vulnerabilities != nil {
		o.Vulnerabilities, changed = *form.Vulnerabilities, true
	}
	if form.Credits != nil {
		o.Credits, changed = *form.Credits, true
	}
	if form.Severity != nil {
		if err := security.CheckSeverity(*form.Severity, o.CVSSVectorString); err != nil {
			damastAPIError(ctx, err)
			return
		}
	}
	var in *security.Input
	if changed {
		if in, err = security.InputFromAPI(ctx, o); err != nil {
			damastAPIError(ctx, err)
			return
		}
	}
	state := ""
	if form.State != nil {
		state = strings.ToLower(strings.TrimSpace(*form.State))
		if err := security.CheckState(state); err != nil { // "" too: for Patch, "" means unchanged
			damastAPIError(ctx, err)
			return
		}
	}
	if err := security.Patch(ctx, repo, ctx.Doer(), a, in, collaborators, state); err != nil {
		damastAPIError(ctx, err)
		return
	}
	damastRespond(ctx, http.StatusOK, a, level)
}

// DamastAdvisoryOSV returns a published advisory as OSV JSON. Unpublished advisories have no OSV document.
func DamastAdvisoryOSV(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/security-advisories/{ghsa_id}/osv repository repoGetSecurityAdvisoryOSV
	// ---
	// summary: Get a published or withdrawn security advisory in OSV format
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
	// - name: ghsa_id
	//   in: path
	//   description: id of the advisory (DAMAST-...)
	//   type: string
	//   required: true
	// responses:
	//   "200":
	//     "$ref": "#/responses/DamastOSV"
	//   "404":
	//     "$ref": "#/responses/notFound"
	a, _ := damastAPIAdvisory(ctx)
	if !a.IsPublished() {
		ctx.NotFound()
		return
	}
	o, err := security.ToOSV(ctx, ctx.Repo().Repository, a)
	if err != nil {
		ctx.InternalServerError(err)
		return
	}
	ctx.JSON(http.StatusOK, o)
}

// DamastListAdvisoriesOSV returns all published advisories of the repository as a JSON array of OSV documents.
func DamastListAdvisoriesOSV(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/security-advisories/osv repository repoListSecurityAdvisoriesOSV
	// ---
	// summary: List the repository's published and withdrawn security advisories in OSV format
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
	// responses:
	//   "200":
	//     "$ref": "#/responses/DamastOSVList"
	//   "404":
	//     "$ref": "#/responses/notFound"
	repo := ctx.Repo().Repository
	doer, p := damastAccess(ctx)
	list, err := security.Visible(ctx, repo, doer, p)
	if err != nil {
		ctx.InternalServerError(err)
		return
	}
	res := make([]*security.OSV, 0, len(list))
	for _, a := range list {
		if !a.IsPublished() {
			continue
		}
		o, err := security.ToOSV(ctx, repo, a)
		if err != nil {
			ctx.InternalServerError(err)
			return
		}
		res = append(res, o)
	}
	ctx.JSON(http.StatusOK, res)
}

// DamastListAdvisoryComments lists the private discussion.
func DamastListAdvisoryComments(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/security-advisories/{ghsa_id}/comments repository repoListSecurityAdvisoryComments
	// ---
	// summary: List the private discussion of a security advisory
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
	// - name: ghsa_id
	//   in: path
	//   description: id of the advisory (DAMAST-...)
	//   type: string
	//   required: true
	// - name: page
	//   in: query
	//   description: page number of results to return (1-based)
	//   type: integer
	// - name: per_page
	//   in: query
	//   description: page size of results (GitHub's name; limit works too)
	//   type: integer
	// - name: limit
	//   in: query
	//   description: page size of results
	//   type: integer
	// responses:
	//   "200":
	//     "$ref": "#/responses/DamastAdvisoryCommentList"
	//   "404":
	//     "$ref": "#/responses/notFound"
	a, _ := damastAPIAdvisory(ctx)
	comments, err := damast.AdvisoryComments(ctx, a.ID)
	if err != nil {
		ctx.InternalServerError(err)
		return
	}
	comments = damastPage(ctx, comments)
	ids := make([]int64, 0, len(comments))
	for _, c := range comments {
		ids = append(ids, c.PosterID)
	}
	users, err := security.LoadUsers(ctx, ids...)
	if err != nil {
		ctx.InternalServerError(err)
		return
	}
	res := make([]*security.APIComment, 0, len(comments))
	for _, c := range comments {
		res = append(res, security.ToAPIComment(ctx, c, users[c.PosterID], ctx.Doer()))
	}
	ctx.JSON(http.StatusOK, res)
}

// DamastCreateAdvisoryComment adds a comment to the private discussion.
func DamastCreateAdvisoryComment(ctx *context.APIContext) {
	// swagger:operation POST /repos/{owner}/{repo}/security-advisories/{ghsa_id}/comments repository repoCreateSecurityAdvisoryComment
	// ---
	// summary: Comment in the private discussion of a security advisory
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
	// - name: ghsa_id
	//   in: path
	//   description: id of the advisory (DAMAST-...)
	//   type: string
	//   required: true
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/DamastAdvisoryCommentOption"
	// responses:
	//   "201":
	//     "$ref": "#/responses/DamastAdvisoryComment"
	//   "404":
	//     "$ref": "#/responses/notFound"
	//   "422":
	//     "$ref": "#/responses/validationError"
	a, _ := damastAPIAdvisory(ctx)
	form := web.GetForm(ctx).(*security.APICommentOption)
	c, err := security.AddComment(ctx, ctx.Repo().Repository, ctx.Doer(), a, form.Body)
	if err != nil {
		damastAPIError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, security.ToAPIComment(ctx, c, ctx.Doer(), ctx.Doer()))
}

// DamastAdvisoryCommentAssignment loads a comment of the loaded advisory that the doer may edit (its poster
// or an admin), or answers 404.
func DamastAdvisoryCommentAssignment(ctx *context.APIContext) {
	a, level := damastAPIAdvisory(ctx)
	c, err := damast.GetAdvisoryComment(ctx, a.ID, ctx.ParamsInt64("comment_id"))
	if errors.Is(err, util.ErrNotExist) || (err == nil && !security.CanEditComment(c, ctx.Doer(), level)) {
		ctx.NotFound()
		return
	} else if err != nil {
		ctx.InternalServerError(err)
		return
	}
	ctx.Data["DamastComment"] = c
}

// DamastEditAdvisoryComment edits a comment of the private discussion.
func DamastEditAdvisoryComment(ctx *context.APIContext) {
	// swagger:operation PATCH /repos/{owner}/{repo}/security-advisories/{ghsa_id}/comments/{comment_id} repository repoEditSecurityAdvisoryComment
	// ---
	// summary: Edit a comment of the private discussion (its author or a repository admin)
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
	// - name: ghsa_id
	//   in: path
	//   description: id of the advisory (DAMAST-...)
	//   type: string
	//   required: true
	// - name: comment_id
	//   in: path
	//   description: id of the comment
	//   type: integer
	//   format: int64
	//   required: true
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/DamastAdvisoryCommentOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/DamastAdvisoryComment"
	//   "404":
	//     "$ref": "#/responses/notFound"
	//   "422":
	//     "$ref": "#/responses/validationError"
	c := ctx.Data["DamastComment"].(*damast.AdvisoryComment)
	form := web.GetForm(ctx).(*security.APICommentOption)
	if err := security.EditComment(ctx, c, form.Body); err != nil {
		damastAPIError(ctx, err)
		return
	}
	users, err := security.LoadUsers(ctx, c.PosterID)
	if err != nil {
		ctx.InternalServerError(err)
		return
	}
	ctx.JSON(http.StatusOK, security.ToAPIComment(ctx, c, users[c.PosterID], ctx.Doer()))
}

// DamastDeleteAdvisoryComment deletes a comment of the private discussion.
func DamastDeleteAdvisoryComment(ctx *context.APIContext) {
	// swagger:operation DELETE /repos/{owner}/{repo}/security-advisories/{ghsa_id}/comments/{comment_id} repository repoDeleteSecurityAdvisoryComment
	// ---
	// summary: Delete a comment of the private discussion (its author or a repository admin)
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
	// - name: ghsa_id
	//   in: path
	//   description: id of the advisory (DAMAST-...)
	//   type: string
	//   required: true
	// - name: comment_id
	//   in: path
	//   description: id of the comment
	//   type: integer
	//   format: int64
	//   required: true
	// responses:
	//   "204":
	//     "$ref": "#/responses/empty"
	//   "404":
	//     "$ref": "#/responses/notFound"
	if err := security.DeleteComment(ctx, ctx.Data["DamastComment"].(*damast.AdvisoryComment)); err != nil {
		ctx.InternalServerError(err)
		return
	}
	ctx.Status(http.StatusNoContent)
}
