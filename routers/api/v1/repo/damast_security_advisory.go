// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"errors"
	"net/http"

	"forgejo.org/models/damast"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/util"
	"forgejo.org/modules/web"
	"forgejo.org/services/context"
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

// DamastAdvisoryReqRead lets only users in who may read the repository's code (or admins).
func DamastAdvisoryReqRead(ctx *context.APIContext) {
	if !security.CanReadRepo(ctx.Permission()) {
		ctx.NotFound()
	}
}

// DamastAdvisoryReqAdmin lets only repository admins in.
func DamastAdvisoryReqAdmin(ctx *context.APIContext) {
	if !security.IsAdmin(ctx.Permission()) {
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
	level, err := security.AccessLevel(ctx, ctx.Repo().Repository, a, ctx.Doer(), ctx.Permission())
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
	// responses:
	//   "200":
	//     "$ref": "#/responses/DamastSecurityAdvisoryList"
	//   "404":
	//     "$ref": "#/responses/notFound"
	repo := ctx.Repo().Repository
	list, err := security.Visible(ctx, repo, ctx.Doer(), ctx.Permission())
	if err != nil {
		ctx.InternalServerError(err)
		return
	}
	state := ctx.FormString("state")
	res := make([]*security.APIAdvisory, 0, len(list))
	for _, a := range list {
		if state != "" && a.State != state {
			continue
		}
		level, err := security.AccessLevel(ctx, repo, a, ctx.Doer(), ctx.Permission())
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
	ok, err := security.CanReport(ctx, ctx.Repo().Repository, ctx.Doer(), security.CanReadRepo(ctx.Permission()))
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
	set(&o.CveID, form.CveID)
	if form.CWEIDs != nil {
		o.CWEIDs, changed = *form.CWEIDs, true
	}
	if form.Vulnerabilities != nil {
		o.Vulnerabilities, changed = *form.Vulnerabilities, true
	}
	if form.Credits != nil {
		o.Credits, changed = *form.Credits, true
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
		state = *form.State
		if state == "" {
			damastAPIError(ctx, &security.InputError{Key: "damast.security.error.state", Args: []any{a.State, state}})
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
	list, err := security.Visible(ctx, repo, ctx.Doer(), ctx.Permission())
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
