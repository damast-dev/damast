// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package damast

import (
	"context"

	"forgejo.org/models/db"

	"xorm.io/builder"
)

// Deleted packages, repositories and owners take their Damast package rows with them: IDs can come back
// (SQLite reuses the highest rowid), and a new package or account must not inherit an old setting.

// DeletePackagePolicy removes the policy of a deleted package.
func DeletePackagePolicy(ctx context.Context, packageID int64) error {
	_, err := db.GetEngine(ctx).ID(packageID).Delete(&PackagePolicy{})
	return err
}

// DeleteRepoPackagePolicies removes the policies of packages linked to a deleted repository: they fall back
// to Forgejo's owner rules instead of inheriting from whatever repository they get linked to next.
func DeleteRepoPackagePolicies(ctx context.Context, repoID int64) error {
	return deletePackagePolicies(ctx, builder.Eq{"repo_id": repoID})
}

// DeleteOwnerPackageData removes a deleted user's or organization's package settings and the policies of
// its packages.
func DeleteOwnerPackageData(ctx context.Context, ownerID int64) error {
	if err := deletePackagePolicies(ctx, builder.Eq{"owner_id": ownerID}); err != nil {
		return err
	}
	_, err := db.GetEngine(ctx).ID(ownerID).Delete(&OwnerPackagePolicy{})
	return err
}

func deletePackagePolicies(ctx context.Context, packageCond builder.Cond) error {
	_, err := db.GetEngine(ctx).Where(builder.In("package_id", builder.Select("id").From("package").Where(packageCond))).
		Delete(&PackagePolicy{})
	return err
}
