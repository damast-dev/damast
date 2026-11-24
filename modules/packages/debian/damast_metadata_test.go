// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package debian

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// GO-2026-6905: a negative ar member size made blakesmith/ar panic in Reader.Read.
func TestDamastParsePackageNegativeMemberSize(t *testing.T) {
	var buf bytes.Buffer
	buf.WriteString("!<arch>\n")
	fmt.Fprintf(&buf, "%-16s%-12s%-6s%-6s%-8s%-10s`\n", controlTar+".gz", "0", "0", "0", "100644", "-10")
	buf.WriteString("payload")

	assert.NotPanics(t, func() {
		_, err := ParsePackage(&buf)
		assert.ErrorIs(t, err, ErrDamastInvalidArchive)
	})
}
