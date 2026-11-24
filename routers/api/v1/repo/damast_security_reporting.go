// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package repo

import (
	"net/http"

	"forgejo.org/models/damast"
	"forgejo.org/services/context"
	"forgejo.org/services/damast/security"
)

// The private vulnerability reporting switch, shaped like GitHub's
// (https://docs.github.com/en/rest/repos/repos#check-if-private-vulnerability-reporting-is-enabled-for-a-repository).
// Repository admins only; everyone else gets a 404.

// DamastGetPrivateReporting tells whether the repository accepts private vulnerability reports.
func DamastGetPrivateReporting(ctx *context.APIContext) {
	// swagger:operation GET /repos/{owner}/{repo}/private-vulnerability-reporting repository repoGetPrivateVulnerabilityReporting
	// ---
	// summary: Check if private vulnerability reporting is enabled (repository admins)
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
	//     "$ref": "#/responses/DamastPrivateReporting"
	//   "404":
	//     "$ref": "#/responses/notFound"
	enabled, err := damast.PrivateReportingEnabled(ctx, ctx.Repo().Repository)
	if err != nil {
		ctx.InternalServerError(err)
		return
	}
	ctx.JSON(http.StatusOK, security.APIPrivateReporting{Enabled: enabled})
}

// DamastEnablePrivateReporting turns private vulnerability reporting on.
func DamastEnablePrivateReporting(ctx *context.APIContext) {
	// swagger:operation PUT /repos/{owner}/{repo}/private-vulnerability-reporting repository repoEnablePrivateVulnerabilityReporting
	// ---
	// summary: Enable private vulnerability reporting (repository admins)
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
	//   "204":
	//     "$ref": "#/responses/empty"
	//   "404":
	//     "$ref": "#/responses/notFound"
	//   "423":
	//     "$ref": "#/responses/error"
	damastSetPrivateReporting(ctx, true)
}

// DamastDisablePrivateReporting turns private vulnerability reporting off.
func DamastDisablePrivateReporting(ctx *context.APIContext) {
	// swagger:operation DELETE /repos/{owner}/{repo}/private-vulnerability-reporting repository repoDisablePrivateVulnerabilityReporting
	// ---
	// summary: Disable private vulnerability reporting (repository admins)
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
	//   "204":
	//     "$ref": "#/responses/empty"
	//   "404":
	//     "$ref": "#/responses/notFound"
	//   "423":
	//     "$ref": "#/responses/error"
	damastSetPrivateReporting(ctx, false)
}

func damastSetPrivateReporting(ctx *context.APIContext, enabled bool) {
	if err := damast.SetPrivateReporting(ctx, ctx.Repo().Repository, enabled); err != nil {
		ctx.InternalServerError(err)
		return
	}
	ctx.Status(http.StatusNoContent)
}
