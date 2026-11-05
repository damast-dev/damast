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
// Not in the swagger spec: that file is generated from upstream code and would conflict on every sync.

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
	a, level := damastAPIAdvisory(ctx)
	damastRespond(ctx, http.StatusOK, a, level)
}

// DamastUpdateAdvisory edits an advisory, changes its state or its collaborators (repository admins).
func DamastUpdateAdvisory(ctx *context.APIContext) {
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
	set := func(dst *string, src *string) {
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
	if changed {
		in, err := security.InputFromAPI(ctx, o)
		if err == nil {
			err = security.Update(ctx, a, in)
		}
		if err != nil {
			damastAPIError(ctx, err)
			return
		}
	}
	if collaborators != nil {
		if err := security.SetCollaborators(ctx, repo, ctx.Doer(), a, collaborators); err != nil {
			damastAPIError(ctx, err)
			return
		}
	}
	if form.State != nil {
		if err := security.SetState(ctx, repo, ctx.Doer(), a, *form.State); err != nil {
			damastAPIError(ctx, err)
			return
		}
	}
	damastRespond(ctx, http.StatusOK, a, level)
}

// DamastAdvisoryOSV returns a published advisory as OSV JSON. Unpublished advisories have no OSV document.
func DamastAdvisoryOSV(ctx *context.APIContext) {
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
	a, _ := damastAPIAdvisory(ctx)
	form := web.GetForm(ctx).(*security.APICommentOption)
	c, err := security.AddComment(ctx, ctx.Repo().Repository, ctx.Doer(), a, form.Body)
	if err != nil {
		damastAPIError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, security.ToAPIComment(ctx, c, ctx.Doer(), ctx.Doer()))
}
