// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package packages

import (
	"context"
	"errors"
)

// DamastCheckRead is set by services/damast/packages; it rejects reads under provisional read access
// unless the reader may read the package's linked repository. A hook variable avoids an import cycle.
var DamastCheckRead func(ctx context.Context, p *Package) error

// DamastCheckReadFile does the same for a single file, for lookups that skip the package descriptor
// (container HEAD/GET of blobs and manifests).
var DamastCheckReadFile func(ctx context.Context, pf *PackageFile) error

// DamastCheckReadPackageID does the same by package ID; version and file lists use it to drop hidden
// packages, so they look missing instead of answering with a different error.
var DamastCheckReadPackageID func(ctx context.Context, packageID int64) error

// DamastReadGrant reports whether the request reads under provisional read access.
var DamastReadGrant func(ctx context.Context) bool

// DamastHidden reports, under provisional read access, whether the named package is hidden from the reader
// or missing; lookups that query properties directly (conan revisions) then answer like for a missing one.
func DamastHidden(ctx context.Context, ownerID int64, packageType Type, name string) (bool, error) {
	if DamastReadGrant == nil || !DamastReadGrant(ctx) {
		return false, nil
	}
	_, err := GetPackageByName(ctx, ownerID, packageType, name)
	if errors.Is(err, ErrPackageNotExist) {
		return true, nil
	}
	return false, err
}
