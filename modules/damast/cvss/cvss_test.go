// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package cvss

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Expected scores as published by NVD / the FIRST CVSS v3.1 calculator.
func TestScore(t *testing.T) {
	for vector, want := range map[string]float64{
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H": 9.8,
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:C/C:H/I:H/A:H": 10.0,
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:R/S:C/C:L/I:L/A:N": 6.1,
		"CVSS:3.1/AV:N/AC:L/PR:L/UI:N/S:C/C:L/I:L/A:N": 6.4,
		"CVSS:3.1/AV:L/AC:L/PR:L/UI:N/S:U/C:H/I:H/A:H": 7.8,
		"CVSS:3.1/AV:N/AC:H/PR:N/UI:N/S:U/C:H/I:N/A:N": 5.9,
		"CVSS:3.1/AV:P/AC:H/PR:H/UI:R/S:U/C:L/I:N/A:N": 1.6,
		"CVSS:3.1/AV:N/AC:L/PR:H/UI:N/S:C/C:H/I:H/A:H": 9.1,
		"CVSS:3.1/AV:A/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:H": 6.5,
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:N": 0,
		// metric order does not change the score
		"CVSS:3.1/S:U/AV:N/AC:L/PR:N/UI:N/C:H/I:H/A:H": 9.8,
	} {
		got, err := Score(vector)
		require.NoError(t, err, vector)
		assert.InDelta(t, want, got, 1e-9, vector)
	}
}

func TestScoreInvalid(t *testing.T) {
	for _, vector := range []string{
		"",
		"AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H",           // no prefix
		"CVSS:3.0/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H",  // only 3.1
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H",      // missing A
		"CVSS:3.1/AV:X/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H",  // bad value
		"CVSS:3.1/AV:N/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H", // duplicate
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H/E:F",
		"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H/",
	} {
		_, err := Score(vector)
		assert.Error(t, err, vector)
	}
}

func TestSeverity(t *testing.T) {
	for score, want := range map[float64]string{
		0: "none", 0.1: "low", 3.9: "low", 4: "medium", 6.9: "medium", 7: "high", 8.9: "high", 9: "critical", 10: "critical",
	} {
		assert.Equal(t, want, Severity(score), score)
	}
}
