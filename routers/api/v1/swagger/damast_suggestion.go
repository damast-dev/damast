// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package swagger

import "forgejo.org/routers/api/v1/repo"

// Swagger responses and request bodies of Damast's review suggestions API.

// DamastSuggestionList
// swagger:response DamastSuggestionList
type swaggerResponseDamastSuggestionList struct {
	// in:body
	Body []repo.DamastSuggestion `json:"body"`
}

// DamastApplySuggestionsResult
// swagger:response DamastApplySuggestionsResult
type swaggerResponseDamastApplySuggestionsResult struct {
	// in:body
	Body repo.DamastApplySuggestionsResult `json:"body"`
}

// damastSuggestionParameterBodies makes go-swagger include the request body (not actually a response).
// swagger:response damastSuggestionParameterBodies
type swaggerDamastSuggestionParameterBodies struct {
	// in:body
	DamastApplySuggestionsOption repo.DamastApplySuggestionsOption
}
