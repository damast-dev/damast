// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package v1

import (
	"forgejo.org/modules/web"
	"forgejo.org/routers/api/v1/repo"
	"forgejo.org/services/damast/security"
)

// damastSecurityAdvisoryRoutes adds /repos/{owner}/{repo}/security-advisories. Only reqAnyRepoReader is a
// recorded permission middleware (an existing sequence for TestAPIv1Permissions); the advisory checks are
// Damast's own and answer 404, also for anonymous write requests. Keep the route table of
// tests/integration/damast_security_test.go in sync.
func damastSecurityAdvisoryRoutes(m *web.Route) {
	m.Group("/security-advisories", func() {
		m.Get("", repo.DamastListAdvisories)
		m.Post("", repo.DamastAdvisoryReqAdmin, bind(security.APICreateOption{}), repo.DamastCreateAdvisory)
		m.Post("/reports", repo.DamastAdvisoryReqReporter, bind(security.APIReportOption{}), repo.DamastReportVulnerability)
		m.Get("/osv", repo.DamastListAdvisoriesOSV)
		m.Group("/{ghsa_id}", func() {
			m.Get("", repo.DamastGetAdvisory)
			m.Patch("", repo.DamastAdvisoryReq(security.Admin), bind(security.APIUpdateOption{}), repo.DamastUpdateAdvisory)
			m.Get("/osv", repo.DamastAdvisoryOSV)
			m.Get("/comments", repo.DamastAdvisoryReq(security.Participant), repo.DamastListAdvisoryComments)
			m.Post("/comments", repo.DamastAdvisoryReq(security.Participant), bind(security.APICommentOption{}), repo.DamastCreateAdvisoryComment)
		}, repo.DamastAdvisoryAssignment)
	}, reqAnyRepoReader(), repo.DamastAdvisoryReqRead)
}
