// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package cvss computes CVSS v3.1 base scores (https://www.first.org/cvss/v3.1/specification-document).
package cvss

import (
	"fmt"
	"math"
	"strings"
)

var weights = map[string]map[string]float64{
	"AV": {"N": 0.85, "A": 0.62, "L": 0.55, "P": 0.2},
	"AC": {"L": 0.77, "H": 0.44},
	"PR": {"N": 0.85, "L": 0.62, "H": 0.27}, // L and H change when the scope changes
	"UI": {"N": 0.85, "R": 0.62},
	"S":  {"U": 0, "C": 0},
	"C":  {"H": 0.56, "L": 0.22, "N": 0},
	"I":  {"H": 0.56, "L": 0.22, "N": 0},
	"A":  {"H": 0.56, "L": 0.22, "N": 0},
}

// Score parses a CVSS v3.1 base vector ("CVSS:3.1/AV:N/AC:L/...") and returns its base score.
// Temporal and environmental metrics are rejected: the score shown must be the base score.
func Score(vector string) (float64, error) {
	rest, ok := strings.CutPrefix(vector, "CVSS:3.1/")
	if !ok {
		return 0, fmt.Errorf("vector must start with CVSS:3.1/")
	}
	m := map[string]string{}
	for _, part := range strings.Split(rest, "/") {
		k, v, _ := strings.Cut(part, ":")
		if _, known := weights[k][v]; !known {
			return 0, fmt.Errorf("unknown metric %q", part)
		}
		if _, dup := m[k]; dup {
			return 0, fmt.Errorf("metric %s given twice", k)
		}
		m[k] = v
	}
	if len(m) != len(weights) {
		return 0, fmt.Errorf("all eight base metrics (AV, AC, PR, UI, S, C, I, A) are required")
	}

	changed := m["S"] == "C"
	pr := weights["PR"][m["PR"]]
	if changed && m["PR"] == "L" {
		pr = 0.68
	} else if changed && m["PR"] == "H" {
		pr = 0.5
	}
	iss := 1 - (1-weights["C"][m["C"]])*(1-weights["I"][m["I"]])*(1-weights["A"][m["A"]])
	impact := 6.42 * iss
	if changed {
		impact = 7.52*(iss-0.029) - 3.25*math.Pow(iss-0.02, 15)
	}
	if impact <= 0 {
		return 0, nil
	}
	exploitability := 8.22 * weights["AV"][m["AV"]] * weights["AC"][m["AC"]] * pr * weights["UI"][m["UI"]]
	if changed {
		return roundup(math.Min(1.08*(impact+exploitability), 10)), nil
	}
	return roundup(math.Min(impact+exploitability, 10)), nil
}

// roundup is the floating point safe Roundup of CVSS v3.1 (Appendix A).
func roundup(x float64) float64 {
	i := int64(math.Round(x * 100000))
	if i%10000 == 0 {
		return float64(i) / 100000
	}
	return float64(i/10000+1) / 10
}

// Severity is the qualitative rating of a score: none, low, medium, high or critical.
func Severity(score float64) string {
	switch {
	case score >= 9:
		return "critical"
	case score >= 7:
		return "high"
	case score >= 4:
		return "medium"
	case score > 0:
		return "low"
	}
	return "none"
}
