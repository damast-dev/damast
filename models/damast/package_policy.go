// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package damast holds Damast-only tables. Forgejo ignores them, so they can be dropped when going back.
package damast

import (
	"context"

	"forgejo.org/models/db"
	"forgejo.org/modules/timeutil"
)

// PackagePolicy stores Damast settings of a package. No row means plain Forgejo behaviour.
type PackagePolicy struct {
	PackageID         int64              `xorm:"pk"`
	InheritRepoAccess bool               `xorm:"NOT NULL DEFAULT false"`
	CreatedUnix       timeutil.TimeStamp `xorm:"created"`
	UpdatedUnix       timeutil.TimeStamp `xorm:"updated"`
}

// TableName keeps all Damast tables under the damast_ prefix.
func (PackagePolicy) TableName() string { return "damast_package_policy" }

func init() {
	// Registered models are created by SyncAllTables on every start, no migration needed.
	db.RegisterModel(new(PackagePolicy))
}

// InheritsRepoAccess reports whether write access to the package follows its linked repository.
func InheritsRepoAccess(ctx context.Context, packageID int64) (bool, error) {
	p := &PackagePolicy{}
	has, err := db.GetEngine(ctx).ID(packageID).Get(p)
	return has && p.InheritRepoAccess, err
}

// SetInheritRepoAccess creates or updates the policy of a package.
func SetInheritRepoAccess(ctx context.Context, packageID int64, inherit bool) error {
	e := db.GetEngine(ctx)
	has, err := e.ID(packageID).Exist(&PackagePolicy{})
	if err != nil {
		return err
	}
	if has {
		_, err = e.ID(packageID).Cols("inherit_repo_access").Update(&PackagePolicy{InheritRepoAccess: inherit})
		return err
	}
	_, err = e.Insert(&PackagePolicy{PackageID: packageID, InheritRepoAccess: inherit})
	return err
}
