// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package setting

import (
	"forgejo.org/modules/setting"
	shared "forgejo.org/routers/web/shared/packages"
	"forgejo.org/services/context"
)

// DamastPackagesPost saves the user's Damast package settings.
func DamastPackagesPost(ctx *context.Context) {
	shared.DamastSettingsPost(ctx, ctx.Doer, setting.AppSubURL+"/user/settings/packages")
}
