// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package swagger

import (
	api "forgejo.org/modules/structs"
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

// DamastStack
// swagger:response DamastStack
type swaggerResponseDamastStack struct {
	// in:body
	Body api.DamastStack `json:"body"`
}

// DamastStackActionResult
// swagger:response DamastStackActionResult
type swaggerResponseDamastStackActionResult struct {
	// in:body
	Body api.DamastStackActionResult `json:"body"`
}

// not actually a response, just a hack to get go-swagger to include the Damast option structs
// swagger:response damastStackParameterBodies
type swaggerDamastStackParameterBodies struct {
	// in:body
	DamastStackActionOption api.DamastStackActionOption
}
