// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package packages

import (
	"net/http"

	"forgejo.org/models/damast"
	"forgejo.org/modules/json"
	"forgejo.org/services/context"
)

// DamastPackageSettings are an owner's Damast package settings.
// NewPackages says who may create new package names: "owners" (owner-level package writers, the default)
// or "repo_writers" (also repository writers and Actions runs, in registries GitHub Packages has).
type DamastPackageSettings struct {
	NewPackages string `json:"new_packages"`
}

const (
	damastNewPackagesOwners      = "owners"
	damastNewPackagesRepoWriters = "repo_writers"
)

// DamastGetSettings answers GET /api/v1/damast/packages/{username}/settings.
func DamastGetSettings(ctx *context.APIContext) {
	damastServeSettings(ctx)
}

// DamastEditSettings answers PATCH /api/v1/damast/packages/{username}/settings.
func DamastEditSettings(ctx *context.APIContext) {
	var form DamastPackageSettings
	if err := json.NewDecoder(ctx.Req.Body).Decode(&form); err != nil {
		ctx.Error(http.StatusUnprocessableEntity, "DamastEditSettings", err)
		return
	}
	if form.NewPackages != damastNewPackagesOwners && form.NewPackages != damastNewPackagesRepoWriters {
		ctx.Error(http.StatusUnprocessableEntity, "DamastEditSettings", `new_packages must be "owners" or "repo_writers"`)
		return
	}
	if err := damast.SetRepoWritersMayCreatePackages(ctx, ctx.Package().Owner.ID, form.NewPackages == damastNewPackagesRepoWriters); err != nil {
		ctx.InternalServerError(err)
		return
	}
	damastServeSettings(ctx)
}

func damastServeSettings(ctx *context.APIContext) {
	allow, err := damast.RepoWritersMayCreatePackages(ctx, ctx.Package().Owner.ID)
	if err != nil {
		ctx.InternalServerError(err)
		return
	}
	s := DamastPackageSettings{NewPackages: damastNewPackagesOwners}
	if allow {
		s.NewPackages = damastNewPackagesRepoWriters
	}
	ctx.JSON(http.StatusOK, s)
}
