// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package swagger

import "forgejo.org/services/damast/security"

// Swagger responses and request bodies of Damast's security advisories API.

// DamastSecurityAdvisory
// swagger:response DamastSecurityAdvisory
type swaggerResponseDamastSecurityAdvisory struct {
	// in:body
	Body security.APIAdvisory `json:"body"`
}

// DamastSecurityAdvisoryList
// swagger:response DamastSecurityAdvisoryList
type swaggerResponseDamastSecurityAdvisoryList struct {
	// in:body
	Body []security.APIAdvisory `json:"body"`
}

// DamastOSV
// swagger:response DamastOSV
type swaggerResponseDamastOSV struct {
	// in:body
	Body security.OSV `json:"body"`
}

// DamastOSVList
// swagger:response DamastOSVList
type swaggerResponseDamastOSVList struct {
	// in:body
	Body []security.OSV `json:"body"`
}

// DamastAdvisoryComment
// swagger:response DamastAdvisoryComment
type swaggerResponseDamastAdvisoryComment struct {
	// in:body
	Body security.APIComment `json:"body"`
}

// DamastAdvisoryCommentList
// swagger:response DamastAdvisoryCommentList
type swaggerResponseDamastAdvisoryCommentList struct {
	// in:body
	Body []security.APIComment `json:"body"`
}

// damastParameterBodies makes go-swagger include the request bodies (not actually a response).
// swagger:response damastParameterBodies
type swaggerDamastParameterBodies struct {
	// in:body
	DamastAdvisoryCreateOption security.APICreateOption
	// in:body
	DamastAdvisoryReportOption security.APIReportOption
	// in:body
	DamastAdvisoryUpdateOption security.APIUpdateOption
	// in:body
	DamastAdvisoryCommentOption security.APICommentOption
}
