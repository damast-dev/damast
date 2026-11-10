// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package web

import (
	"forgejo.org/modules/web"
	"forgejo.org/routers/web/repo"
	"forgejo.org/services/context"
	"forgejo.org/services/damast/security"
)

// registerDamastSecurityRoutes adds the Security tab. Keep tests/integration/damast_security_test.go's route
// table in sync: every route here must have a row there.
func registerDamastSecurityRoutes(m *web.Route) {
	w := context.RepoMustNotBeArchived() // archived repositories: advisories can be read, not changed
	m.Group("/{username}/{reponame}/security", func() {
		m.Get("", repo.DamastSecurity)
		m.Post("/settings", repo.DamastSecurityReqAdmin, w, repo.DamastSecuritySettingsPost)
		m.Combo("/report", repo.DamastSecurityReqReporter).Get(repo.DamastSecurityReport).Post(repo.DamastSecurityReportPost)
		m.Combo("/advisories/new", repo.DamastSecurityReqAdmin, w).Get(repo.DamastAdvisoryNew).Post(repo.DamastAdvisoryNewPost)
		m.Group("/advisories/{id}", func() {
			m.Get("", repo.DamastAdvisoryView)
			m.Combo("/edit", repo.DamastAdvisoryReq(security.Admin), w).Get(repo.DamastAdvisoryEdit).Post(repo.DamastAdvisoryEditPost)
			m.Post("/state", repo.DamastAdvisoryReq(security.Admin), w, repo.DamastAdvisoryStatePost)
			m.Post("/comments", repo.DamastAdvisoryReq(security.Participant), w, repo.DamastAdvisoryCommentPost)
			m.Group("/comments/{comment_id}", func() {
				m.Post("/edit", repo.DamastAdvisoryCommentEditPost)
				m.Post("/delete", repo.DamastAdvisoryCommentDeletePost)
			}, repo.DamastAdvisoryReq(security.Participant), w, repo.DamastAdvisoryCommentAssignment)
			m.Post("/collaborators", repo.DamastAdvisoryReq(security.Admin), w, repo.DamastAdvisoryCollaboratorPost)
			m.Post("/collaborators/remove", repo.DamastAdvisoryReq(security.Admin), w, repo.DamastAdvisoryCollaboratorRemovePost)
		}, repo.DamastAdvisoryAssignment)
	}, ignSignIn, context.RepoAssignment, context.UnitTypes(), repo.DamastSecurityReqRead)
}
