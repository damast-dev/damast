// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package security

import (
	"context"
	"slices"
	"strings"
	"time"

	"forgejo.org/models/damast"
	"forgejo.org/models/db"
	access_model "forgejo.org/models/perm/access"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/timeutil"
)

// Report caps. A real reporter rarely has more than a couple of open reports per repository; the caps stop
// one account from flooding a repository's admins (per repository) or many repositories (per day). They are
// counts, not a time-window rate limiter, so they need no state outside the advisory table and no settings.
const (
	MaxOpenReportsPerRepo = 3  // reports still in triage, per reporter and repository
	MaxReportsPerDay      = 10 // reports per reporter in the last 24 hours, instance-wide
)

// ErrTooManyReports is returned when a reporter hits one of the caps.
var ErrTooManyReports = &InputError{Key: "damast.security.error.too_many_reports"}

func apply(a *damast.SecurityAdvisory, in *Input) {
	a.Title = in.Title
	a.Description = in.Description
	a.Severity = in.Severity
	a.CVSSVector = in.CVSSVector
	a.CVEID = in.CVEID
	a.CWEIDs = in.CWEIDs
	a.Affected = in.Affected
	a.Credits = in.Credits
}

// Create adds a draft advisory written by a repository admin. The caller checks IsAdmin.
func Create(ctx context.Context, repo *repo_model.Repository, doer *user_model.User, in *Input) (*damast.SecurityAdvisory, error) {
	if err := in.Normalize(); err != nil {
		return nil, err
	}
	a := &damast.SecurityAdvisory{RepoID: repo.ID, State: damast.AdvisoryDraft, CreatorID: doer.ID}
	apply(a, in)
	return a, damast.CreateAdvisory(ctx, a)
}

// Report adds a private vulnerability report (state triage) and notifies the repository admins.
// The caller checks CanReport.
func Report(ctx context.Context, repo *repo_model.Repository, doer *user_model.User, in *Input) (*damast.SecurityAdvisory, error) {
	in.Credits = nil // reporters cannot credit; maintainers do that when accepting
	in.CVEID = ""
	if err := in.Normalize(); err != nil {
		return nil, err
	}
	open, err := damast.CountReports(ctx, doer.ID, repo.ID, damast.AdvisoryTriage, time.Time{})
	if err != nil {
		return nil, err
	}
	recent, err := damast.CountReports(ctx, doer.ID, 0, "", time.Now().Add(-24*time.Hour))
	if err != nil {
		return nil, err
	}
	if open >= MaxOpenReportsPerRepo || recent >= MaxReportsPerDay {
		return nil, ErrTooManyReports
	}
	a := &damast.SecurityAdvisory{RepoID: repo.ID, State: damast.AdvisoryTriage, CreatorID: doer.ID, ReporterID: doer.ID}
	apply(a, in)
	if err := damast.CreateAdvisory(ctx, a); err != nil {
		return nil, err
	}
	notifyNewReport(ctx, repo, a, doer)
	return a, nil
}

// CanReport reports whether doer may submit a private vulnerability report: signed in, may read the code,
// reporting is on, the repository is not archived and its owner has not blocked doer.
func CanReport(ctx context.Context, repo *repo_model.Repository, doer *user_model.User, readsRepo bool) (bool, error) {
	if doer == nil || doer.ID <= 0 || !readsRepo || repo.IsArchived || user_model.IsBlocked(ctx, repo.OwnerID, doer.ID) {
		return false, nil
	}
	return damast.PrivateReportingEnabled(ctx, repo.ID)
}

// Update replaces the content of an advisory. Editing a published advisory moves its OSV "modified" time.
func Update(ctx context.Context, a *damast.SecurityAdvisory, in *Input) error {
	if err := in.Normalize(); err != nil {
		return err
	}
	apply(a, in)
	return damast.UpdateAdvisory(ctx, a, "title", "description", "severity", "cvss_vector", "cve_id", "cwe_ids", "affected", "credits")
}

// transitions lists the allowed state changes, as on GitHub: published can only be withdrawn, withdrawn is final.
var transitions = map[string][]string{
	damast.AdvisoryTriage:    {damast.AdvisoryDraft, damast.AdvisoryClosed},
	damast.AdvisoryDraft:     {damast.AdvisoryPublished, damast.AdvisoryClosed},
	damast.AdvisoryClosed:    {damast.AdvisoryDraft},
	damast.AdvisoryPublished: {damast.AdvisoryWithdrawn},
}

// SetState moves an advisory to a new state and notifies the reporter and collaborators.
func SetState(ctx context.Context, repo *repo_model.Repository, doer *user_model.User, a *damast.SecurityAdvisory, state string) error {
	notify, err := setState(ctx, repo, doer, a, state)
	if err == nil {
		notify()
	}
	return err
}

// setState saves the new state and returns the notification to send once the change is committed.
func setState(ctx context.Context, repo *repo_model.Repository, doer *user_model.User, a *damast.SecurityAdvisory, state string) (func(), error) {
	state = strings.ToLower(strings.TrimSpace(state))
	if state == a.State {
		return func() {}, nil
	}
	if !slices.Contains(transitions[a.State], state) {
		return nil, invalid("state", a.State, state)
	}
	old := a.State
	a.State = state
	cols := []string{"state"}
	switch state {
	case damast.AdvisoryPublished:
		a.PublishedUnix = timeutil.TimeStampNow()
		cols = append(cols, "published_unix")
	case damast.AdvisoryClosed:
		a.ClosedUnix = timeutil.TimeStampNow()
		cols = append(cols, "closed_unix")
	case damast.AdvisoryWithdrawn:
		a.WithdrawnUnix = timeutil.TimeStampNow()
		cols = append(cols, "withdrawn_unix")
	}
	if ok, err := damast.UpdateAdvisoryState(ctx, a, old, cols...); err != nil || !ok {
		a.State = old
		if err == nil { // someone else changed the state since a was loaded
			err = invalid("state", old, state)
		}
		return nil, err
	}
	return func() { notifyParticipants(ctx, repo, a, doer, stateMessage(old, state), "") }, nil
}

func stateMessage(old, state string) string {
	switch {
	case old == damast.AdvisoryTriage && state == damast.AdvisoryDraft:
		return "accepted"
	case state == damast.AdvisoryPublished:
		return "published"
	case state == damast.AdvisoryClosed:
		return "closed"
	case state == damast.AdvisoryWithdrawn:
		return "withdrawn"
	default:
		return "reopened"
	}
}

// AddComment adds a comment to the private discussion. The caller checks the doer is at least Participant.
func AddComment(ctx context.Context, repo *repo_model.Repository, doer *user_model.User, a *damast.SecurityAdvisory, content string) (*damast.AdvisoryComment, error) {
	content = strings.TrimSpace(content)
	if content == "" || len(content) > maxDescription {
		return nil, invalid("comment", maxDescription)
	}
	c := &damast.AdvisoryComment{AdvisoryID: a.ID, PosterID: doer.ID, Content: content}
	if err := damast.CreateAdvisoryComment(ctx, c); err != nil {
		return nil, err
	}
	notifyParticipants(ctx, repo, a, doer, "commented", content)
	return c, nil
}

// CanEditComment reports whether the doer may edit or delete the comment: its poster, or an admin.
// The caller has checked that the doer is at least Participant.
func CanEditComment(c *damast.AdvisoryComment, doer *user_model.User, level Level) bool {
	return level >= Admin || (doer != nil && doer.ID > 0 && c.PosterID == doer.ID)
}

// EditComment replaces the content of a comment. Edits notify nobody.
func EditComment(ctx context.Context, c *damast.AdvisoryComment, content string) error {
	content = strings.TrimSpace(content)
	if content == "" || len(content) > maxDescription {
		return invalid("comment", maxDescription)
	}
	c.Content = content
	return damast.UpdateAdvisoryComment(ctx, c)
}

// DeleteComment deletes a comment.
func DeleteComment(ctx context.Context, c *damast.AdvisoryComment) error {
	return damast.DeleteAdvisoryComment(ctx, c)
}

// Patch applies an API update in one transaction: new content (nil: unchanged), the complete list of
// collaborators (nil: unchanged) and a new state (empty: unchanged). If one step fails nothing is saved and
// nobody is notified.
func Patch(ctx context.Context, repo *repo_model.Repository, doer *user_model.User, a *damast.SecurityAdvisory, in *Input, collaborators []*user_model.User, state string) error {
	var notify []func()
	err := db.WithTx(ctx, func(ctx context.Context) error {
		if in != nil {
			if err := Update(ctx, a, in); err != nil {
				return err
			}
		}
		if collaborators != nil {
			n, err := setCollaborators(ctx, repo, doer, a, collaborators)
			if err != nil {
				return err
			}
			notify = append(notify, n)
		}
		if state != "" {
			n, err := setState(ctx, repo, doer, a, state)
			if err != nil {
				return err
			}
			notify = append(notify, n)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, n := range notify {
		n()
	}
	return nil
}

// checkCollaborator rejects users who could not use the access: AccessLevel requires code read access, and
// the admin should learn that now instead of the collaborator getting a 404 later.
func checkCollaborator(ctx context.Context, repo *repo_model.Repository, u *user_model.User) error {
	if u == nil || u.ID <= 0 || u.IsOrganization() || !u.IsActive || u.ProhibitLogin {
		return invalid("collaborator")
	}
	perm, err := access_model.GetUserRepoPermission(ctx, repo, u)
	if err != nil {
		return err
	}
	if !CanReadRepo(&perm) {
		return invalid("collaborator_access", u.Name)
	}
	return nil
}

// AddCollaborator gives u access to the unpublished advisory and its discussion, and tells them.
func AddCollaborator(ctx context.Context, repo *repo_model.Repository, doer *user_model.User, a *damast.SecurityAdvisory, u *user_model.User) error {
	return SetCollaborators(ctx, repo, doer, a, nil, u)
}

// RemoveCollaborator takes the access away again.
func RemoveCollaborator(ctx context.Context, a *damast.SecurityAdvisory, u *user_model.User) error {
	return damast.RemoveAdvisoryCollaborator(ctx, a.ID, u.ID)
}

// SetCollaborators adds the given users; with replace set (API: collaborating_users) it also removes everyone
// else. Nothing changes if one of the users is rejected. New collaborators are notified.
func SetCollaborators(ctx context.Context, repo *repo_model.Repository, doer *user_model.User, a *damast.SecurityAdvisory, replace []*user_model.User, add ...*user_model.User) error {
	notify, err := setCollaborators(ctx, repo, doer, a, replace, add...)
	if err == nil {
		notify()
	}
	return err
}

// setCollaborators saves the change and returns the notification to send once it is committed.
func setCollaborators(ctx context.Context, repo *repo_model.Repository, doer *user_model.User, a *damast.SecurityAdvisory, replace []*user_model.User, add ...*user_model.User) (func(), error) {
	users := append(slices.Clone(replace), add...)
	for _, u := range users {
		if err := checkCollaborator(ctx, repo, u); err != nil {
			return nil, err
		}
	}
	var added []*user_model.User
	err := db.WithTx(ctx, func(ctx context.Context) error {
		current, err := damast.AdvisoryCollaboratorIDs(ctx, a.ID)
		if err != nil {
			return err
		}
		if replace != nil {
			for _, id := range current {
				if !slices.ContainsFunc(users, func(u *user_model.User) bool { return u.ID == id }) {
					if err := damast.RemoveAdvisoryCollaborator(ctx, a.ID, id); err != nil {
						return err
					}
				}
			}
		}
		for _, u := range users {
			if !slices.Contains(current, u.ID) {
				current = append(current, u.ID)
				if err := damast.AddAdvisoryCollaborator(ctx, a.ID, u.ID); err != nil {
					return err
				}
				added = append(added, u)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return func() {
		for _, u := range added {
			notifyAddedCollaborator(ctx, repo, a, doer, u)
		}
	}, nil
}

// LoadUsers returns the users with the given ids; deleted users become the ghost user.
func LoadUsers(ctx context.Context, ids ...int64) (map[int64]*user_model.User, error) {
	users, err := user_model.GetPossibleUserByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	m := make(map[int64]*user_model.User, len(ids))
	for _, u := range users {
		m[u.ID] = u
	}
	for _, id := range ids {
		if m[id] == nil {
			m[id] = user_model.NewGhostUser()
		}
	}
	return m, nil
}
