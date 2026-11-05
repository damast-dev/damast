// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	stdCtx "context"
	"errors"
	"net/http"
	"strconv"
	"time"

	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/modules/setting"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/web"
	"forgejo.org/services/context"
	damast_stack "forgejo.org/services/damast/stack"
)

func damastLoadStack(ctx *context.APIContext, c stdCtx.Context) *damast_stack.Stack {
	pr, err := issues_model.GetPullRequestByIndex(ctx, ctx.Repo().Repository.ID, ctx.ParamsInt64(":index"))
	if err != nil {
		if issues_model.IsErrPullRequestNotExist(err) {
			ctx.NotFound()
		} else {
			ctx.Error(http.StatusInternalServerError, "GetPullRequestByIndex", err)
		}
		return nil
	}
	if err := pr.LoadIssue(ctx); err != nil {
		ctx.Error(http.StatusInternalServerError, "LoadIssue", err)
		return nil
	}
	pr.BaseRepo, pr.Issue.Repo = ctx.Repo().Repository, ctx.Repo().Repository
	s, err := damast_stack.Find(c, pr)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "damast_stack.Find", err)
		return nil
	}
	return s
}

// GetPullRequestDamastStack returns the stack a pull request belongs to
func GetPullRequestDamastStack(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/pulls/{index}/damast/stack repository repoGetPullRequestDamastStack
	// ---
	// summary: Get the stack of a pull request (Damast)
	// description: Layers are the open same-repository pull requests that build on each other
	//   (a layer's base branch is the head branch of the layer below). A pull request that is
	//   not stacked returns a stack with one layer.
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
	//     "$ref": "#/responses/DamastStack"
	//   "404":
	//     "$ref": "#/responses/notFound"
	s := damastLoadStack(ctx, ctx)
	if s == nil {
		return
	}
	status, err := s.LoadStatus(ctx)
	if err != nil {
		ctx.Error(http.StatusInternalServerError, "LoadStatus", err)
		return
	}
	res := &api.DamastStack{Trunk: s.Trunk, Current: s.Current.PR.Index, Layers: make([]*api.DamastStackLayer, 0, len(s.Layers))}
	for _, l := range s.Layers {
		st := status[l.PR.ID]
		layer := &api.DamastStackLayer{
			Number:  l.PR.Index,
			Title:   l.PR.Issue.Title,
			HTMLURL: l.PR.Issue.HTMLURL(),
			State:   string(api.StateOpen),
			Draft:   st.Draft,
			Base:    l.PR.BaseBranch,
			Head:    l.PR.HeadBranch,
			HeadSHA: st.HeadSHA,
			Depth:   l.Depth,
			Checks:  st.Checks,
		}
		if l.PR.HasMerged {
			layer.State = "merged"
		} else if l.PR.Issue.IsClosed {
			layer.State = string(api.StateClosed)
		}
		if l.Parent != nil {
			layer.Parent = l.Parent.PR.Index
		}
		res.Layers = append(res.Layers, layer)
	}
	ctx.JSON(http.StatusOK, res)
}

func damastStackAction(ctx *context.APIContext, run func(stdCtx.Context, *damast_stack.Stack, map[int64]string, repo_model.MergeStyle) ([]*issues_model.PullRequest, error)) {
	form := web.GetForm(ctx).(*api.DamastStackActionOption)
	heads := make(map[int64]string, len(form.Heads))
	for k, v := range form.Heads {
		n, err := strconv.ParseInt(k, 10, 64)
		if err != nil {
			ctx.Error(http.StatusUnprocessableEntity, "heads", "heads must be keyed by pull request number")
			return
		}
		heads[n] = v
	}
	switch repo_model.MergeStyle(form.Style) {
	case "", repo_model.MergeStyleMerge, repo_model.MergeStyleRebase, repo_model.MergeStyleRebaseMerge, repo_model.MergeStyleSquash, repo_model.MergeStyleFastForwardOnly:
	default:
		ctx.Error(http.StatusUnprocessableEntity, "style", "invalid merge style")
		return
	}
	// once started, finish even if the client goes away
	workCtx, cancel := stdCtx.WithTimeout(stdCtx.WithoutCancel(ctx), time.Duration(setting.Git.Timeout.Default)*time.Second)
	defer cancel()
	s := damastLoadStack(ctx, workCtx)
	if s == nil {
		return
	}
	done, err := run(workCtx, s, heads, repo_model.MergeStyle(form.Style))
	res := &api.DamastStackActionResult{Done: make([]int64, 0, len(done))}
	for _, pr := range done {
		res.Done = append(res.Done, pr.Index)
	}
	if err != nil {
		var layerErr *damast_stack.LayerError
		if !errors.As(err, &layerErr) {
			ctx.Error(http.StatusInternalServerError, "damast stack", err)
			return
		}
		res.FailedAt, res.Message = layerErr.PR.Index, layerErr.Message(ctx.Locale)
		status := http.StatusConflict
		if errors.Is(err, damast_stack.ErrNotAllowed) {
			status = http.StatusForbidden
		}
		ctx.JSON(status, res)
		return
	}
	ctx.JSON(http.StatusOK, res)
}

// DamastRebasePullRequestStack rebases every layer of a stack onto its parent
func DamastRebasePullRequestStack(ctx *context.APIContext) {
	// swagger:operation POST /repos/{owner}/{repo}/pulls/{index}/damast/stack/rebase repository repoRebasePullRequestDamastStack
	// ---
	// summary: Rebase the whole stack of a pull request (Damast)
	// description: Rebases every layer, bottom first, onto the current head of the layer below
	//   (the bottom layer onto the trunk), with a force-push guarded by the head commit the caller
	//   saw. Requires the right to update every layer by rebase. Stops at the first conflict or moved branch;
	//   layers rebased before that stay rebased.
	// consumes:
	// - application/json
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
	//   description: index of any pull request in the stack
	//   type: integer
	//   format: int64
	//   required: true
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/DamastStackActionOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/DamastStackActionResult"
	//   "403":
	//     "$ref": "#/responses/DamastStackActionResult"
	//   "404":
	//     "$ref": "#/responses/notFound"
	//   "409":
	//     "$ref": "#/responses/DamastStackActionResult"
	//   "422":
	//     "$ref": "#/responses/validationError"
	damastStackAction(ctx, func(c stdCtx.Context, s *damast_stack.Stack, heads map[int64]string, _ repo_model.MergeStyle) ([]*issues_model.PullRequest, error) {
		return damast_stack.Rebase(c, s, ctx.Doer(), heads)
	})
}

// DamastMergePullRequestStack merges a stack from the bottom up to a pull request
func DamastMergePullRequestStack(ctx *context.APIContext) {
	// swagger:operation POST /repos/{owner}/{repo}/pulls/{index}/damast/stack/merge repository repoMergePullRequestDamastStack
	// ---
	// summary: Merge a stack from the bottom up to this pull request (Damast)
	// description: Merges the layers below this pull request and the pull request itself, bottom
	//   first, each through the normal merge checks (permissions, approvals, required checks,
	//   work-in-progress, conflicts). Each layer is retargeted to the trunk before it is merged.
	//   Stops at the first layer that cannot be merged; layers merged before that stay merged.
	// consumes:
	// - application/json
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
	//   description: index of the topmost pull request to merge
	//   type: integer
	//   format: int64
	//   required: true
	// - name: body
	//   in: body
	//   required: true
	//   schema:
	//     "$ref": "#/definitions/DamastStackActionOption"
	// responses:
	//   "200":
	//     "$ref": "#/responses/DamastStackActionResult"
	//   "404":
	//     "$ref": "#/responses/notFound"
	//   "409":
	//     "$ref": "#/responses/DamastStackActionResult"
	//   "422":
	//     "$ref": "#/responses/validationError"
	damastStackAction(ctx, func(c stdCtx.Context, s *damast_stack.Stack, heads map[int64]string, style repo_model.MergeStyle) ([]*issues_model.PullRequest, error) {
		return damast_stack.MergeUpTo(c, s, s.Current, ctx.Doer(), style, heads)
	})
}
