// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package security implements repository security advisories and private vulnerability reporting.
//
// Deny by default: AccessLevel is the only place that decides who sees an advisory. Every web and API route,
// every list and every notification recipient goes through it. Advisories live in damast_* tables only and are
// never turned into issues, comments, actions, notifications or webhook payloads, so Forgejo's feeds, search,
// counters and cross-references cannot see them.
package security

import (
	"context"

	"forgejo.org/models/damast"
	access_model "forgejo.org/models/perm/access"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/models/unit"
	user_model "forgejo.org/models/user"
)

// Level is what a user may do with one advisory. Levels are ordered.
type Level int

const (
	None        Level = iota // must get a 404
	Viewer                   // may read a published advisory (not its comments)
	Participant              // reporter or advisory collaborator: reads everything, comments
	Admin                    // repository admin, org owner or site admin: everything
)

// IsAdmin reports whether the permission manages advisories of the repository. Site admins, org owners and
// admin teams all have at least admin access mode in their repository permission.
func IsAdmin(perm *access_model.Permission) bool {
	return perm != nil && perm.IsAdmin()
}

// CanReadRepo reports whether the user may see published advisories and report vulnerabilities at all.
// Advisories describe code, so code read access is required (admins always pass).
func CanReadRepo(perm *access_model.Permission) bool {
	return IsAdmin(perm) || (perm != nil && perm.CanRead(unit.TypeCode))
}

// AccessLevel decides what doer (nil for anonymous) with the repository permission perm may do with a.
func AccessLevel(ctx context.Context, repo *repo_model.Repository, a *damast.SecurityAdvisory, doer *user_model.User, perm *access_model.Permission) (Level, error) {
	if repo == nil || a == nil || perm == nil || a.RepoID != repo.ID {
		return None, nil
	}
	if IsAdmin(perm) {
		return Admin, nil
	}
	if !CanReadRepo(perm) {
		return None, nil // includes reporters and collaborators who lost access to the repository
	}
	if doer != nil && doer.ID > 0 { // not anonymous, ghost or the Actions user
		if a.ReporterID == doer.ID {
			return Participant, nil
		}
		ok, err := damast.IsAdvisoryCollaborator(ctx, a.ID, doer.ID)
		if err != nil {
			return None, err
		}
		if ok {
			return Participant, nil
		}
	}
	if a.IsPublished() {
		return Viewer, nil
	}
	return None, nil
}

// UserLevel is AccessLevel for a user without a request, e.g. a notification recipient.
func UserLevel(ctx context.Context, repo *repo_model.Repository, a *damast.SecurityAdvisory, u *user_model.User) (Level, error) {
	if u == nil || u.ID <= 0 || !u.IsActive || u.ProhibitLogin {
		return None, nil
	}
	perm, err := access_model.GetUserRepoPermission(ctx, repo, u)
	if err != nil {
		return None, err
	}
	return AccessLevel(ctx, repo, a, u, &perm)
}

// Visible returns the advisories of the repository the doer may see (at least Viewer), newest first.
func Visible(ctx context.Context, repo *repo_model.Repository, doer *user_model.User, perm *access_model.Permission) ([]*damast.SecurityAdvisory, error) {
	if !CanReadRepo(perm) {
		return nil, nil
	}
	// shortcut: loads all advisories of the repository and filters in Go; fine for tens per repository,
	// move the filter into SQL if repositories ever have thousands.
	all, err := damast.RepoAdvisories(ctx, repo.ID)
	if err != nil {
		return nil, err
	}
	visible := make([]*damast.SecurityAdvisory, 0, len(all))
	for _, a := range all {
		level, err := AccessLevel(ctx, repo, a, doer, perm)
		if err != nil {
			return nil, err
		}
		if level >= Viewer {
			visible = append(visible, a)
		}
	}
	return visible, nil
}
