// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package v1

import (
	"forgejo.org/modules/web"
	"forgejo.org/routers/api/v1/repo"
	"forgejo.org/services/damast/security"
)

// damastSecurityAdvisoryRoutes adds /repos/{owner}/{repo}/security-advisories and /private-vulnerability-reporting. Only reqAnyRepoReader is a
// recorded permission middleware (an existing sequence for TestAPIv1Permissions); the advisory checks are
// Damast's own and answer 404, also for anonymous write requests. Keep the route table of
// tests/integration/damast_security_test.go in sync.
func damastSecurityAdvisoryRoutes(m *web.Route) {
	w := repo.DamastReqWritable // archived repositories: 423 for writes, after the access checks
	m.Group("/security-advisories", func() {
		m.Get("", repo.DamastListAdvisories)
		m.Post("", repo.DamastAdvisoryReqAdmin, w, bind(security.APICreateOption{}), repo.DamastCreateAdvisory)
		m.Post("/reports", repo.DamastAdvisoryReqReporter, bind(security.APIReportOption{}), repo.DamastReportVulnerability)
		m.Get("/osv", repo.DamastListAdvisoriesOSV)
		m.Group("/{ghsa_id}", func() {
			m.Get("", repo.DamastGetAdvisory)
			m.Patch("", repo.DamastAdvisoryReq(security.Admin), w, bind(security.APIUpdateOption{}), repo.DamastUpdateAdvisory)
			m.Get("/osv", repo.DamastAdvisoryOSV)
			m.Get("/comments", repo.DamastAdvisoryReq(security.Participant), repo.DamastListAdvisoryComments)
			m.Post("/comments", repo.DamastAdvisoryReq(security.Participant), w, bind(security.APICommentOption{}), repo.DamastCreateAdvisoryComment)
			m.Group("/comments/{comment_id}", func() {
				m.Patch("", w, bind(security.APICommentOption{}), repo.DamastEditAdvisoryComment)
				m.Delete("", w, repo.DamastDeleteAdvisoryComment)
			}, repo.DamastAdvisoryReq(security.Participant), repo.DamastAdvisoryCommentAssignment)
		}, repo.DamastAdvisoryAssignment)
	}, reqAnyRepoReader(), repo.DamastAdvisoryReqRead)
	m.Group("/private-vulnerability-reporting", func() {
		m.Get("", repo.DamastGetPrivateReporting)
		m.Put("", w, repo.DamastEnablePrivateReporting)
		m.Delete("", w, repo.DamastDisablePrivateReporting)
	}, reqAnyRepoReader(), repo.DamastAdvisoryReqAdmin)
}
