// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package packages

import (
	"forgejo.org/models/damast"
	user_model "forgejo.org/models/user"
	"forgejo.org/services/context"
)

// damastSetPackagesContext adds the owner's Damast package settings to the packages settings page.
func damastSetPackagesContext(ctx *context.Context, owner *user_model.User) {
	allow, err := damast.RepoWritersMayCreatePackages(ctx, owner.ID)
	if err != nil {
		ctx.ServerError("RepoWritersMayCreatePackages", err)
		return
	}
	ctx.Data["DamastRepoWritersCreate"] = allow
}

// DamastSettingsPost saves "who may create new packages" (new_packages: owners or repo_writers).
func DamastSettingsPost(ctx *context.Context, owner *user_model.User, redirectTo string) {
	if err := damast.SetRepoWritersMayCreatePackages(ctx, owner.ID, ctx.FormString("new_packages") == "repo_writers"); err != nil {
		ctx.ServerError("SetRepoWritersMayCreatePackages", err)
		return
	}
	ctx.Flash.Success(ctx.Tr("damast.packages.new_packages_saved"))
	ctx.Redirect(redirectTo)
}
