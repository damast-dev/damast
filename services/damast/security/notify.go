// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package security

import (
	"bytes"
	"context"
	"fmt"
	"html/template"

	"forgejo.org/models/damast"
	"forgejo.org/models/db"
	"forgejo.org/models/perm"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/translation"
	"forgejo.org/services/mailer"
)

// Notifications are e-mails only. Forgejo's notification table has no place for an object that is not an
// issue, pull request, commit or repository, and a row there would show up wrongly after going back to Forgejo.
// Every recipient is checked with UserLevel right before sending, so a mail never reaches anyone who could
// not open the advisory. Mentions in advisories and comments are rendered but never notify anyone.

var mailTemplate = template.Must(template.New("advisory").Parse(`<!DOCTYPE html>
<html><body>
<p>{{.Action}}</p>
{{if .Body}}<pre style="white-space: pre-wrap; font-family: inherit">{{.Body}}</pre>{{end}}
<p><a href="{{.Link}}">{{.ID}}: {{.Title}}</a></p>
<p style="color: #666">{{.Footer}}</p>
</body></html>`))

// repoAdmins returns the users with admin access to the repository: owner, org owners, admin teams and admin
// collaborators (all of them are in the access table except the owner of a personal repository).
func repoAdmins(ctx context.Context, repo *repo_model.Repository) ([]*user_model.User, error) {
	var ids []int64
	if err := db.GetEngine(ctx).Table("access").Where("repo_id = ? AND mode >= ?", repo.ID, perm.AccessModeAdmin).Cols("user_id").Find(&ids); err != nil {
		return nil, err
	}
	if err := repo.LoadOwner(ctx); err != nil {
		return nil, err
	}
	if !repo.Owner.IsOrganization() {
		ids = append(ids, repo.OwnerID)
	}
	return user_model.GetUserByIDs(ctx, ids)
}

// send mails everyone in to who has at least the level, except the actor. action is the last part of a
// locale key (damast.security.mail.*) that gets the actor's name.
func send(ctx context.Context, repo *repo_model.Repository, a *damast.SecurityAdvisory, actor *user_model.User, to []*user_model.User, min Level, action, body string) {
	if setting.MailService == nil {
		return
	}
	seen := map[int64]bool{actor.ID: true}
	var msgs []*mailer.Message
	for _, u := range to {
		if u == nil || seen[u.ID] || u.Email == "" || u.EmailNotificationsPreference == user_model.EmailNotificationsDisabled {
			continue
		}
		seen[u.ID] = true
		level, err := UserLevel(ctx, repo, a, u)
		if err != nil {
			log.Error("security: level of %d: %v", u.ID, err)
			continue
		}
		if level < min {
			continue
		}
		locale := translation.NewLocale(u.Language)
		var buf bytes.Buffer
		if err := mailTemplate.Execute(&buf, map[string]any{
			"Action": locale.TrString("damast.security.mail."+action, actor.Name), "Body": body, "Link": HTMLURL(repo, a),
			"ID": a.AdvisoryID, "Title": a.Title, "Footer": locale.TrString("damast.security.mail.footer", repo.FullName()),
		}); err != nil {
			log.Error("security: mail template: %v", err)
			return
		}
		msg := mailer.NewMessage(u.EmailTo(), fmt.Sprintf("[%s] %s (%s)", repo.FullName(), a.Title, a.AdvisoryID), buf.String())
		msg.Info = fmt.Sprintf("UID: %d, security advisory %s", u.ID, a.AdvisoryID)
		msgs = append(msgs, msg)
	}
	if len(msgs) > 0 {
		mailer.SendAsync(msgs...)
	}
}

func notifyNewReport(ctx context.Context, repo *repo_model.Repository, a *damast.SecurityAdvisory, reporter *user_model.User) {
	admins, err := repoAdmins(ctx, repo)
	if err != nil {
		log.Error("security: repo admins: %v", err)
		return
	}
	send(ctx, repo, a, reporter, admins, Admin, "new_report", "")
}

// notifyParticipants tells the reporter and collaborators. When the actor is not an admin (the reporter or a
// collaborator wrote), the admins are told too, so that a reporter's answer reaches the maintainers.
func notifyParticipants(ctx context.Context, repo *repo_model.Repository, a *damast.SecurityAdvisory, actor *user_model.User, action, body string) {
	ids, err := damast.AdvisoryCollaboratorIDs(ctx, a.ID)
	if err != nil {
		log.Error("security: collaborators: %v", err)
		return
	}
	if a.ReporterID > 0 {
		ids = append(ids, a.ReporterID)
	}
	to, err := user_model.GetUserByIDs(ctx, ids)
	if err != nil {
		log.Error("security: users: %v", err)
		return
	}
	if level, err := UserLevel(ctx, repo, a, actor); err == nil && level < Admin {
		admins, err := repoAdmins(ctx, repo)
		if err != nil {
			log.Error("security: repo admins: %v", err)
			return
		}
		to = append(to, admins...)
	}
	send(ctx, repo, a, actor, to, Participant, action, body)
}

func notifyAddedCollaborator(ctx context.Context, repo *repo_model.Repository, a *damast.SecurityAdvisory, actor, u *user_model.User) {
	send(ctx, repo, a, actor, []*user_model.User{u}, Participant, "added_collaborator", "")
}

// HTMLURL is the web page of an advisory.
func HTMLURL(repo *repo_model.Repository, a *damast.SecurityAdvisory) string {
	return repo.HTMLURL() + "/security/advisories/" + a.AdvisoryID
}
