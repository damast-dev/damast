// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"bytes"
	"errors"
	"html/template"
	"net/http"
	"path"
	"strings"

	"forgejo.org/models/damast"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/base"
	"forgejo.org/modules/damast/cvss"
	"forgejo.org/modules/log"
	"forgejo.org/modules/markup"
	"forgejo.org/modules/markup/markdown"
	"forgejo.org/modules/util"
	"forgejo.org/services/context"
	"forgejo.org/services/damast/security"
)

// Security tab: policy, advisories and private vulnerability reports. Every denial is a plain 404, for
// anonymous users too, so nobody can tell an unpublished advisory from one that does not exist.

const (
	tplDamastSecurity     base.TplName = "repo/damast/security/overview"
	tplDamastAdvisory     base.TplName = "repo/damast/security/advisory"
	tplDamastAdvisoryForm base.TplName = "repo/damast/security/form"
)

func damastSecurityCommon(ctx *context.Context) {
	ctx.Data["Title"] = ctx.Tr("damast.security.title")
	ctx.Data["PageIsDamastSecurity"] = true
	ctx.Data["DamastSecurityAdmin"] = security.IsAdmin(&ctx.Repo.Permission)
}

// DamastSecurityReqRead lets only users who may read the repository's code (or admins) in.
func DamastSecurityReqRead(ctx *context.Context) {
	if !security.CanReadRepo(&ctx.Repo.Permission) {
		ctx.NotFound("", nil)
	}
}

// DamastSecurityReqAdmin lets only repository admins in.
func DamastSecurityReqAdmin(ctx *context.Context) {
	if !security.IsAdmin(&ctx.Repo.Permission) {
		ctx.NotFound("", nil)
	}
}

// DamastSecurityReqReporter lets only users in who may submit a private vulnerability report.
func DamastSecurityReqReporter(ctx *context.Context) {
	ok, err := security.CanReport(ctx, ctx.Repo.Repository, ctx.Doer, security.CanReadRepo(&ctx.Repo.Permission))
	if err != nil {
		ctx.ServerError("CanReport", err)
		return
	}
	if !ok {
		ctx.NotFound("", nil)
	}
}

// DamastAdvisoryAssignment loads the advisory of the URL and the doer's level, or answers 404.
func DamastAdvisoryAssignment(ctx *context.Context) {
	a, err := damast.GetAdvisory(ctx, ctx.Repo.Repository.ID, ctx.Params("id"))
	if errors.Is(err, util.ErrNotExist) {
		ctx.NotFound("", nil)
		return
	} else if err != nil {
		ctx.ServerError("GetAdvisory", err)
		return
	}
	level, err := security.AccessLevel(ctx, ctx.Repo.Repository, a, ctx.Doer, &ctx.Repo.Permission)
	if err != nil {
		ctx.ServerError("AccessLevel", err)
		return
	}
	if level == security.None {
		ctx.NotFound("", nil)
		return
	}
	ctx.Data["DamastAdvisory"] = a
	ctx.Data["DamastLevel"] = level
}

// DamastAdvisoryReq requires at least the level for the advisory loaded by DamastAdvisoryAssignment.
func DamastAdvisoryReq(min security.Level) func(ctx *context.Context) {
	return func(ctx *context.Context) {
		if level, ok := ctx.Data["DamastLevel"].(security.Level); !ok || level < min {
			ctx.NotFound("", nil)
		}
	}
}

func damastAdvisory(ctx *context.Context) (*damast.SecurityAdvisory, security.Level) {
	return ctx.Data["DamastAdvisory"].(*damast.SecurityAdvisory), ctx.Data["DamastLevel"].(security.Level)
}

func damastAdvisoryLink(ctx *context.Context, a *damast.SecurityAdvisory) string {
	return ctx.Repo.RepoLink + "/security/advisories/" + a.AdvisoryID
}

func damastRenderMarkdown(ctx *context.Context, s string) template.HTML {
	html, err := markdown.RenderString(&markup.RenderContext{
		Ctx:   ctx,
		Links: markup.Links{Base: ctx.Repo.RepoLink},
		Metas: ctx.Repo.Repository.ComposeMetas(ctx),
	}, s)
	if err != nil {
		log.Error("RenderString: %v", err)
		return template.HTML(template.HTMLEscapeString(s))
	}
	return html
}

// damastFlashError shows a validation error in the user's language; other errors are server errors.
func damastFlashError(ctx *context.Context, err error, now bool) bool {
	var inputErr *security.InputError
	if errors.As(err, &inputErr) {
		ctx.Flash.Error(ctx.Locale.TrString(inputErr.Key, inputErr.Args...), now)
		return true
	}
	ctx.ServerError("security", err)
	return false
}

// DamastSecurity is the Security tab: policy, reporting and the advisories the doer may see.
func DamastSecurity(ctx *context.Context) {
	damastSecurityCommon(ctx)
	repo := ctx.Repo.Repository

	if !repo.IsEmpty && ctx.Repo.GitRepo != nil {
		if commit, err := ctx.Repo.GitRepo.GetBranchCommit(repo.DefaultBranch); err == nil {
			policyPath, content, err := security.FindPolicy(commit)
			if err != nil {
				ctx.ServerError("FindPolicy", err)
				return
			}
			if policyPath != "" {
				ctx.Data["DamastPolicyPath"] = policyPath
				ctx.Data["DamastPolicyLink"] = repo.Link() + "/src/branch/" + util.PathEscapeSegments(repo.DefaultBranch) + "/" + util.PathEscapeSegments(policyPath)
				_, ctx.Data["DamastPolicy"], err = markupRender(ctx, &markup.RenderContext{
					Ctx:          ctx,
					RelativePath: policyPath,
					Links: markup.Links{
						Base:       ctx.Repo.RepoLink,
						BranchPath: "branch/" + util.PathEscapeSegments(repo.DefaultBranch),
						TreePath:   path.Dir(policyPath),
					},
					Metas:   repo.ComposeDocumentMetas(ctx),
					GitRepo: ctx.Repo.GitRepo,
				}, bytes.NewReader(content))
				if err != nil {
					log.Error("render %s: %v", policyPath, err)
				}
			}
		}
	}

	advisories, err := security.Visible(ctx, repo, ctx.Doer, &ctx.Repo.Permission)
	if err != nil {
		ctx.ServerError("Visible", err)
		return
	}
	ctx.Data["DamastAdvisories"] = advisories

	enabled, err := damast.PrivateReportingEnabled(ctx, repo)
	if err != nil {
		ctx.ServerError("PrivateReportingEnabled", err)
		return
	}
	ctx.Data["DamastReportingEnabled"] = enabled
	canReport, err := security.CanReport(ctx, repo, ctx.Doer, security.CanReadRepo(&ctx.Repo.Permission))
	if err != nil {
		ctx.ServerError("CanReport", err)
		return
	}
	ctx.Data["DamastCanReport"] = canReport
	ctx.HTML(http.StatusOK, tplDamastSecurity)
}

// DamastSecuritySettingsPost turns private vulnerability reporting on or off.
func DamastSecuritySettingsPost(ctx *context.Context) {
	if err := damast.SetPrivateReporting(ctx, ctx.Repo.Repository, ctx.FormBool("private_reporting")); err != nil {
		ctx.ServerError("SetPrivateReporting", err)
		return
	}
	ctx.Flash.Success(ctx.Tr("damast.security.settings_saved"))
	ctx.Redirect(ctx.Repo.RepoLink + "/security")
}

// damastForm is what the advisory form shows.
type damastForm struct {
	Title, Description, Severity, CVSSVector, CVEID, CWEs, Credits string
	Affected                                                       []damast.AffectedProduct
}

func damastFormFromAdvisory(ctx *context.Context, a *damast.SecurityAdvisory) (*damastForm, error) {
	f := &damastForm{
		Title: a.Title, Description: a.Description, Severity: a.Severity, CVSSVector: a.CVSSVector, CVEID: a.CVEID,
		CWEs: strings.Join(a.CWEIDs, ", "), Affected: append([]damast.AffectedProduct{}, a.Affected...),
	}
	public, err := security.PublicCredits(ctx, a)
	if err != nil {
		return nil, err
	}
	credits := make([]string, 0, len(public))
	for _, c := range public {
		credits = append(credits, c.User.Name+":"+c.Type)
	}
	f.Credits = strings.Join(credits, ", ")
	return f, nil
}

// damastFormFromRequest reads the form. Credits are "login" or "login:type", separated by commas.
func damastFormFromRequest(ctx *context.Context) (*damastForm, *security.Input, error) {
	if err := ctx.Req.ParseForm(); err != nil {
		return nil, nil, err
	}
	form := ctx.Req.PostForm
	f := &damastForm{
		Title: form.Get("title"), Description: form.Get("description"), Severity: form.Get("severity"),
		CVSSVector: form.Get("cvss_vector"), CVEID: form.Get("cve_id"), CWEs: form.Get("cwe_ids"), Credits: form.Get("credits"),
	}
	in := &security.Input{
		Title: f.Title, Description: f.Description, Severity: f.Severity, CVSSVector: f.CVSSVector, CVEID: f.CVEID,
		CWEIDs: strings.Split(f.CWEs, ","),
	}
	ecosystems, packages, ranges, patched := form["ecosystem"], form["package"], form["vulnerable"], form["patched"]
	for i := range packages {
		p := damast.AffectedProduct{Package: packages[i]}
		if i < len(ecosystems) {
			p.Ecosystem = ecosystems[i]
		}
		if i < len(ranges) {
			p.VulnerableRange = ranges[i]
		}
		if i < len(patched) {
			p.Patched = patched[i]
		}
		if strings.TrimSpace(p.Package) == "" && strings.TrimSpace(p.VulnerableRange) == "" && strings.TrimSpace(p.Patched) == "" {
			continue // the empty row for adding a product
		}
		f.Affected = append(f.Affected, p)
		in.Affected = append(in.Affected, p)
	}
	for _, c := range strings.Split(f.Credits, ",") {
		login, typ, _ := strings.Cut(strings.TrimSpace(c), ":")
		if login == "" {
			continue
		}
		u, err := security.CreditUser(ctx, login)
		if err != nil {
			return f, in, err
		}
		in.Credits = append(in.Credits, damast.Credit{UserID: u.ID, Type: typ})
	}
	return f, in, nil
}

func damastRenderForm(ctx *context.Context, f *damastForm, mode string) {
	damastSecurityCommon(ctx)
	if f == nil {
		f = &damastForm{Severity: "moderate"}
	}
	ctx.Data["DamastForm"] = f
	ctx.Data["DamastAffectedRows"] = append(append([]damast.AffectedProduct{}, f.Affected...), damast.AffectedProduct{})
	ctx.Data["DamastFormMode"] = mode
	ctx.Data["DamastEcosystems"] = security.EcosystemKeys()
	ctx.Data["DamastSeverities"] = security.Severities
	ctx.HTML(http.StatusOK, tplDamastAdvisoryForm)
}

// DamastAdvisoryNew shows the form for a new draft advisory.
func DamastAdvisoryNew(ctx *context.Context) {
	damastRenderForm(ctx, nil, "new")
}

// DamastAdvisoryNewPost creates a draft advisory.
func DamastAdvisoryNewPost(ctx *context.Context) {
	f, in, err := damastFormFromRequest(ctx)
	if err == nil {
		var a *damast.SecurityAdvisory
		if a, err = security.Create(ctx, ctx.Repo.Repository, ctx.Doer, in); err == nil {
			ctx.Redirect(damastAdvisoryLink(ctx, a))
			return
		}
	}
	if damastFlashError(ctx, err, true) {
		damastRenderForm(ctx, f, "new")
	}
}

// DamastSecurityReport shows the private vulnerability report form.
func DamastSecurityReport(ctx *context.Context) {
	damastRenderForm(ctx, nil, "report")
}

// DamastSecurityReportPost submits a private vulnerability report.
func DamastSecurityReportPost(ctx *context.Context) {
	f, in, err := damastFormFromRequest(ctx)
	if err == nil {
		var a *damast.SecurityAdvisory
		if a, err = security.Report(ctx, ctx.Repo.Repository, ctx.Doer, in); err == nil {
			ctx.Flash.Success(ctx.Tr("damast.security.report_submitted"))
			ctx.Redirect(damastAdvisoryLink(ctx, a))
			return
		}
	}
	if damastFlashError(ctx, err, true) {
		damastRenderForm(ctx, f, "report")
	}
}

// DamastAdvisoryEdit shows the edit form.
func DamastAdvisoryEdit(ctx *context.Context) {
	a, _ := damastAdvisory(ctx)
	f, err := damastFormFromAdvisory(ctx, a)
	if err != nil {
		ctx.ServerError("damastFormFromAdvisory", err)
		return
	}
	damastRenderForm(ctx, f, "edit")
}

// DamastAdvisoryEditPost saves the edit form.
func DamastAdvisoryEditPost(ctx *context.Context) {
	a, _ := damastAdvisory(ctx)
	f, in, err := damastFormFromRequest(ctx)
	if err == nil {
		if err = security.Update(ctx, a, in); err == nil {
			ctx.Redirect(damastAdvisoryLink(ctx, a))
			return
		}
	}
	if damastFlashError(ctx, err, true) {
		damastRenderForm(ctx, f, "edit")
	}
}

type damastComment struct {
	Poster   *user_model.User
	Rendered template.HTML
	Comment  *damast.AdvisoryComment
	CanEdit  bool
}

// DamastAdvisoryView shows an advisory. Comments, reporter and collaborators only for participants.
func DamastAdvisoryView(ctx *context.Context) {
	damastSecurityCommon(ctx)
	a, level := damastAdvisory(ctx)
	ctx.Data["Title"] = a.Title
	ctx.Data["DamastIsParticipant"] = level >= security.Participant
	ctx.Data["DamastIsAdvisoryAdmin"] = level >= security.Admin
	ctx.Data["DamastDescription"] = damastRenderMarkdown(ctx, a.Description)
	if a.CVSSVector != "" {
		score, _ := cvss.Score(a.CVSSVector)
		ctx.Data["DamastScore"] = score
	}

	ids := []int64{a.CreatorID, a.ReporterID}
	var comments []*damast.AdvisoryComment
	var collaborators []int64
	if level >= security.Participant {
		var err error
		if comments, err = damast.AdvisoryComments(ctx, a.ID); err != nil {
			ctx.ServerError("AdvisoryComments", err)
			return
		}
		if collaborators, err = damast.AdvisoryCollaboratorIDs(ctx, a.ID); err != nil {
			ctx.ServerError("AdvisoryCollaboratorIDs", err)
			return
		}
		ids = append(ids, collaborators...)
		for _, c := range comments {
			ids = append(ids, c.PosterID)
		}
	}
	users, err := security.LoadUsers(ctx, ids...)
	if err != nil {
		ctx.ServerError("LoadUsers", err)
		return
	}
	if ctx.Data["DamastCredits"], err = security.PublicCredits(ctx, a); err != nil {
		ctx.ServerError("PublicCredits", err)
		return
	}

	if level >= security.Participant {
		ctx.Data["DamastCreator"] = users[a.CreatorID]
		if a.ReporterID != 0 {
			ctx.Data["DamastReporter"] = users[a.ReporterID]
		}
		list := make([]*user_model.User, 0, len(collaborators))
		for _, id := range collaborators {
			list = append(list, users[id])
		}
		ctx.Data["DamastCollaborators"] = list
		rendered := make([]damastComment, 0, len(comments))
		for _, c := range comments {
			rendered = append(rendered, damastComment{
				Poster: users[c.PosterID], Rendered: damastRenderMarkdown(ctx, c.Content), Comment: c,
				CanEdit: security.CanEditComment(c, ctx.Doer, level),
			})
		}
		ctx.Data["DamastComments"] = rendered
	}
	ctx.HTML(http.StatusOK, tplDamastAdvisory)
}

// DamastAdvisoryStatePost accepts, publishes, closes, reopens or withdraws an advisory.
func DamastAdvisoryStatePost(ctx *context.Context) {
	a, _ := damastAdvisory(ctx)
	if err := security.SetState(ctx, ctx.Repo.Repository, ctx.Doer, a, ctx.FormString("state")); err != nil {
		if !damastFlashError(ctx, err, false) {
			return
		}
	}
	ctx.Redirect(damastAdvisoryLink(ctx, a))
}

// DamastAdvisoryCommentPost adds a comment to the private discussion.
func DamastAdvisoryCommentPost(ctx *context.Context) {
	a, _ := damastAdvisory(ctx)
	if _, err := security.AddComment(ctx, ctx.Repo.Repository, ctx.Doer, a, ctx.FormString("content")); err != nil {
		if !damastFlashError(ctx, err, false) {
			return
		}
	}
	ctx.Redirect(damastAdvisoryLink(ctx, a))
}

func damastFormUser(ctx *context.Context) (*user_model.User, bool) {
	name := strings.TrimSpace(ctx.FormString("username"))
	u, err := user_model.GetUserByName(ctx, name)
	if user_model.IsErrUserNotExist(err) {
		ctx.Flash.Error(ctx.Tr("damast.security.error.unknown_user", name))
		return nil, true
	} else if err != nil {
		ctx.ServerError("GetUserByName", err)
		return nil, false
	}
	return u, true
}

// DamastAdvisoryCollaboratorPost adds a collaborator.
func DamastAdvisoryCollaboratorPost(ctx *context.Context) {
	a, _ := damastAdvisory(ctx)
	u, ok := damastFormUser(ctx)
	if !ok {
		return
	}
	if u != nil {
		if err := security.AddCollaborator(ctx, ctx.Repo.Repository, ctx.Doer, a, u); err != nil && !damastFlashError(ctx, err, false) {
			return
		}
	}
	ctx.Redirect(damastAdvisoryLink(ctx, a))
}

// DamastAdvisoryCollaboratorRemovePost removes a collaborator.
func DamastAdvisoryCollaboratorRemovePost(ctx *context.Context) {
	a, _ := damastAdvisory(ctx)
	u, ok := damastFormUser(ctx)
	if !ok {
		return
	}
	if u != nil {
		if err := security.RemoveCollaborator(ctx, a, u); err != nil {
			ctx.ServerError("RemoveCollaborator", err)
			return
		}
	}
	ctx.Redirect(damastAdvisoryLink(ctx, a))
}

// DamastAdvisoryCommentAssignment loads a comment of the loaded advisory that the doer may edit (its poster
// or an admin), or answers 404.
func DamastAdvisoryCommentAssignment(ctx *context.Context) {
	a, level := damastAdvisory(ctx)
	c, err := damast.GetAdvisoryComment(ctx, a.ID, ctx.ParamsInt64("comment_id"))
	if errors.Is(err, util.ErrNotExist) || (err == nil && !security.CanEditComment(c, ctx.Doer, level)) {
		ctx.NotFound("", nil)
		return
	} else if err != nil {
		ctx.ServerError("GetAdvisoryComment", err)
		return
	}
	ctx.Data["DamastComment"] = c
}

// DamastAdvisoryCommentEditPost edits a comment of the private discussion.
func DamastAdvisoryCommentEditPost(ctx *context.Context) {
	a, _ := damastAdvisory(ctx)
	if err := security.EditComment(ctx, ctx.Data["DamastComment"].(*damast.AdvisoryComment), ctx.FormString("content")); err != nil {
		if !damastFlashError(ctx, err, false) {
			return
		}
	}
	ctx.Redirect(damastAdvisoryLink(ctx, a))
}

// DamastAdvisoryCommentDeletePost deletes a comment of the private discussion.
func DamastAdvisoryCommentDeletePost(ctx *context.Context) {
	a, _ := damastAdvisory(ctx)
	if err := security.DeleteComment(ctx, ctx.Data["DamastComment"].(*damast.AdvisoryComment)); err != nil {
		ctx.ServerError("DeleteComment", err)
		return
	}
	ctx.Redirect(damastAdvisoryLink(ctx, a))
}
