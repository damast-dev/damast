// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package user

import (
	"forgejo.org/models/damast"
	"forgejo.org/modules/log"
	"forgejo.org/services/context"
)

// damastPackageSettingsData adds the Damast package policy to the settings page.
func damastPackageSettingsData(ctx *context.Context) {
	inherit, err := damast.InheritsRepoAccess(ctx, ctx.Package.Descriptor.Package.ID)
	if err != nil {
		log.Error("damast.InheritsRepoAccess: %v", err)
	}
	ctx.Data["DamastInheritRepoAccess"] = inherit
}

// damastPackageInheritPost toggles "inherit access from linked repository".
func damastPackageInheritPost(ctx *context.Context) {
	if err := damast.SetInheritRepoAccess(ctx, ctx.Package.Descriptor.Package.ID, ctx.FormBool("inherit")); err != nil {
		ctx.ServerError("SetInheritRepoAccess", err)
		return
	}
	ctx.Flash.Success(ctx.Tr("damast.packages.inherit_saved"))
	ctx.Redirect(ctx.Link)
}
