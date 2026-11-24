// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package swagger

import (
	"forgejo.org/routers/api/v1/packages"
)

// DamastOwnerPackageSettings
// swagger:response DamastOwnerPackageSettings
type swaggerResponseDamastOwnerPackageSettings struct {
	// in:body
	Body packages.DamastOwnerPackageSettings `json:"body"`
}

// DamastPackageSettings
// swagger:response DamastPackageSettings
type swaggerResponseDamastPackageSettings struct {
	// in:body
	Body packages.DamastPackageSettings `json:"body"`
}
