// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package packages

import "context"

// DamastCheckRead is set by services/damast/packages; it rejects reads under provisional read access
// unless the reader may read the package's linked repository. A hook variable avoids an import cycle.
var DamastCheckRead func(ctx context.Context, p *Package) error

// DamastCheckReadFile does the same for a single file, for lookups that skip the package descriptor
// (container HEAD/GET of blobs and manifests).
var DamastCheckReadFile func(ctx context.Context, pf *PackageFile) error
