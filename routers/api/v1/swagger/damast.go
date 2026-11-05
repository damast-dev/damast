// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package swagger

import (
	api "forgejo.org/modules/structs"
)

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
// swagger:response damastParameterBodies
type swaggerDamastParameterBodies struct {
	// in:body
	DamastStackActionOption api.DamastStackActionOption
}
