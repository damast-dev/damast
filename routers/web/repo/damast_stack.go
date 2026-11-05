// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	stdCtx "context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	issues_model "forgejo.org/models/issues"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	"forgejo.org/services/context"
	damast_stack "forgejo.org/services/damast/stack"
	pull_service "forgejo.org/services/pull"
)

// damastStackData fills the stack map of a pull request page (templates/repo/issue/damast/stack.tmpl).
func damastStackData(ctx *context.Context, pull *issues_model.PullRequest) {
	s, err := damast_stack.Find(ctx, pull)
	if err != nil {
		log.Error("damast stack of %v: %v", pull, err)
		return
	}
	if !s.IsStacked() {
		return
	}
	status, err := s.LoadStatus(ctx)
	if err != nil {
		log.Error("damast stack status of %v: %v", pull, err)
		return
	}
	heads := make([]string, 0, len(s.Layers))
	for _, l := range s.Layers {
		heads = append(heads, fmt.Sprintf("%d:%s", l.PR.Index, status[l.PR.ID].HeadSHA))
	}
	data := map[string]any{"Stack": s, "Status": status, "Heads": strings.Join(heads, ",")}

	if ctx.Doer != nil && !ctx.Repo.Repository.IsArchived {
		if data["CanRebase"], err = s.IsUserAllowedToRebase(ctx, ctx.Doer); err != nil {
			log.Error("damast stack rebase permission: %v", err)
		}
		if s.Current.Depth > 0 {
			ok, err := pull_service.IsUserAllowedToMerge(ctx, s.Root.PR, ctx.Repo.Permission, ctx.Doer)
			if err != nil {
				log.Error("damast stack merge permission: %v", err)
			}
			if prUnit, err := ctx.Repo.Repository.GetUnit(ctx, unit.TypePullRequests); ok && err == nil {
				data["CanMerge"] = true
				data["MergeStyle"] = prUnit.PullRequestsConfig().GetDefaultMergeStyle()
				numbers := []string{}
				for _, l := range s.Path(s.Current) {
					numbers = append(numbers, fmt.Sprintf("#%d", l.PR.Index))
				}
				data["MergeList"] = strings.Join(numbers, ", ")
			}
		}
	}
	ctx.Data["DamastStack"] = data
}

// damastParseHeads parses "1:sha,2:sha" (web form) into PR index -> head commit.
func damastParseHeads(s string) map[int64]string {
	heads := map[int64]string{}
	for _, pair := range strings.Split(s, ",") {
		index, sha, ok := strings.Cut(pair, ":")
		if n, err := strconv.ParseInt(index, 10, 64); ok && err == nil && sha != "" {
			heads[n] = sha
		}
	}
	return heads
}

// damastStackAction runs a stack action and reports the outcome as a flash message.
func damastStackAction(ctx *context.Context, run func(stdCtx.Context, *damast_stack.Stack) ([]*issues_model.PullRequest, error), successKey string) {
	issue, ok := getPullInfo(ctx)
	if !ok {
		return
	}
	// like MergePullRequest: once started, finish even if the browser goes away
	workCtx, cancel := stdCtx.WithTimeout(stdCtx.WithoutCancel(ctx), time.Duration(setting.Git.Timeout.Default)*time.Second)
	defer cancel()

	s, err := damast_stack.Find(workCtx, issue.PullRequest)
	if err != nil {
		ctx.ServerError("damast_stack.Find", err)
		return
	}
	done, err := run(workCtx, s)
	if err != nil {
		var layerErr *damast_stack.LayerError
		if !errors.As(err, &layerErr) {
			ctx.ServerError("damast stack", err)
			return
		}
		ctx.Flash.Error(layerErr.Message(ctx.Locale))
	} else if len(done) == 0 {
		ctx.Flash.Info(ctx.Tr("damast.stack.nothing_to_do"))
	} else {
		numbers := make([]string, 0, len(done))
		for _, pr := range done {
			numbers = append(numbers, fmt.Sprintf("#%d", pr.Index))
		}
		ctx.Flash.Success(ctx.Tr(successKey, strings.Join(numbers, ", ")))
	}
	ctx.Redirect(issue.Link())
}

// DamastStackActionPost runs "Rebase stack" or "Merge up to here".
func DamastStackActionPost(ctx *context.Context) {
	heads := damastParseHeads(ctx.FormString("heads"))
	if ctx.Params("action") == "rebase" {
		damastStackAction(ctx, func(c stdCtx.Context, s *damast_stack.Stack) ([]*issues_model.PullRequest, error) {
			return damast_stack.Rebase(c, s, ctx.Doer, heads)
		}, "damast.stack.rebased")
		return
	}
	style := repo_model.MergeStyle(ctx.FormString("style"))
	damastStackAction(ctx, func(c stdCtx.Context, s *damast_stack.Stack) ([]*issues_model.PullRequest, error) {
		return damast_stack.MergeUpTo(c, s, s.Current, ctx.Doer, style, heads)
	}, "damast.stack.merged")
}
