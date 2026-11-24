// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package org

import (
	"forgejo.org/modules/setting"
	shared "forgejo.org/routers/web/shared/packages"
	"forgejo.org/services/context"
)

// DamastPackagesPost saves the organization's Damast package settings.
func DamastPackagesPost(ctx *context.Context) {
	shared.DamastSettingsPost(ctx, ctx.ContextUser, setting.AppSubURL+"/org/"+ctx.ContextUser.Name+"/settings/packages")
}
