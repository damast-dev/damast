// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package swagger

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// go-swagger keeps only one of two responses with the same name, so the
// request bodies listed in the other silently vanish from the spec.
func TestDamastSwaggerResponseNamesUnique(t *testing.T) {
	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	re := regexp.MustCompile(`swagger:response (\S+)`)
	seen := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			if prev, ok := seen[m[1]]; ok {
				assert.Failf(t, "duplicate swagger:response", "%s in %s and %s", m[1], prev, f)
			}
			seen[m[1]] = f
		}
	}
}
