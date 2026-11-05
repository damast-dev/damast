// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package damast

import (
	"context"

	"forgejo.org/models/db"
	"forgejo.org/modules/timeutil"
)

// OwnerPackagePolicy stores Damast package settings of a user or organization. No row means defaults.
type OwnerPackagePolicy struct {
	OwnerID int64 `xorm:"pk"`
	// RepoWritersCreate lets repository writers and Actions runs create new package names; off by default,
	// so only owner-level package writers can claim a name, as in Forgejo.
	RepoWritersCreate bool               `xorm:"NOT NULL DEFAULT false"`
	CreatedUnix       timeutil.TimeStamp `xorm:"created"`
	UpdatedUnix       timeutil.TimeStamp `xorm:"updated"`
}

// TableName keeps all Damast tables under the damast_ prefix.
func (OwnerPackagePolicy) TableName() string { return "damast_owner_package_policy" }

func init() {
	db.RegisterModel(new(OwnerPackagePolicy))
}

// RepoWritersMayCreatePackages reports whether repository writers may create new packages of the owner.
func RepoWritersMayCreatePackages(ctx context.Context, ownerID int64) (bool, error) {
	p := &OwnerPackagePolicy{}
	has, err := db.GetEngine(ctx).ID(ownerID).Get(p)
	return has && p.RepoWritersCreate, err
}

// SetRepoWritersMayCreatePackages creates or updates the owner's policy.
func SetRepoWritersMayCreatePackages(ctx context.Context, ownerID int64, allow bool) error {
	e := db.GetEngine(ctx)
	has, err := e.ID(ownerID).Exist(&OwnerPackagePolicy{})
	if err != nil {
		return err
	}
	if has {
		_, err = e.ID(ownerID).Cols("repo_writers_create").Update(&OwnerPackagePolicy{RepoWritersCreate: allow})
		return err
	}
	_, err = e.Insert(&OwnerPackagePolicy{OwnerID: ownerID, RepoWritersCreate: allow})
	return err
}
