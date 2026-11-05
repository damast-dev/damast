// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"net/http"
	"strconv"

	"forgejo.org/services/context"
	suggestion_service "forgejo.org/services/damast/suggestion"
)

// DamastApplySuggestions commits review suggestions (form values comment_ids and message) to the head branch.
func DamastApplySuggestions(ctx *context.Context) {
	issue := GetActionIssue(ctx)
	if ctx.Written() {
		return
	}
	if !issue.IsPull {
		ctx.NotFound("DamastApplySuggestions", nil)
		return
	}
	var ids []int64
	for _, s := range ctx.FormStrings("comment_ids") {
		id, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			ctx.JSONError(ctx.Tr("damast.suggestions.err_invalid"))
			return
		}
		ids = append(ids, id)
	}
	sha, err := suggestion_service.Apply(ctx, ctx.Doer, issue.PullRequest, ids, ctx.FormString("message"))
	if err != nil {
		key, status := suggestion_service.ErrorKey(err)
		if key == "" {
			ctx.ServerError("DamastApplySuggestions", err)
			return
		}
		ctx.JSON(status, map[string]any{"errorMessage": ctx.Tr(key), "renderFormat": "text"})
		return
	}
	ctx.JSON(http.StatusOK, map[string]any{"commit": sha})
}
