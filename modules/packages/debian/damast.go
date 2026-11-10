// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package debian

import "forgejo.org/modules/util"

// ErrDamastInvalidArchive rejects ar archives with member headers blakesmith/ar can't read safely.
var ErrDamastInvalidArchive = util.NewInvalidArgumentErrorf("package archive is invalid")
