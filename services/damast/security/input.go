// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package security

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"forgejo.org/models/damast"
	"forgejo.org/modules/damast/cvss"
	"forgejo.org/modules/util"
)

// Ecosystems maps GitHub's ecosystem keys (used in the API and stored) to OSV ecosystem names.
// GitHub's "other" is left out because OSV cannot express it.
var Ecosystems = map[string]string{
	"actions":  "GitHub Actions",
	"composer": "Packagist",
	"erlang":   "Hex",
	"go":       "Go",
	"maven":    "Maven",
	"npm":      "npm",
	"nuget":    "NuGet",
	"pip":      "PyPI",
	"pub":      "Pub",
	"rubygems": "RubyGems",
	"rust":     "crates.io",
	"swift":    "SwiftURL",
}

// EcosystemKeys returns the ecosystem keys sorted, for forms.
func EcosystemKeys() []string {
	keys := make([]string, 0, len(Ecosystems))
	for k := range Ecosystems {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// Severities in ascending order. GitHub's API calls "moderate" "medium"; both are accepted.
var Severities = []string{"low", "moderate", "high", "critical"}

// CreditTypes are the OSV credit types in lower case (GitHub uses the same set).
var CreditTypes = []string{
	"finder", "reporter", "analyst", "coordinator", "remediation_developer", "remediation_reviewer",
	"remediation_verifier", "tool", "sponsor", "other",
}

const (
	maxTitle       = 255
	maxDescription = 65535
	maxList        = 50
)

var (
	cveRe     = regexp.MustCompile(`^CVE-\d{4}-\d{4,}$`)
	cweRe     = regexp.MustCompile(`^CWE-\d{1,6}$`)
	versionRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_~:-]*$`)
	packageRe = regexp.MustCompile(`^[^\s][^\r\n\t]*$`)
)

// Input is the editable content of an advisory or report.
type Input struct {
	Title       string
	Description string
	Severity    string // ignored when CVSSVector is set: the severity follows the score
	CVSSVector  string
	CVEID       string
	CWEIDs      []string
	Affected    []damast.AffectedProduct
	Credits     []damast.Credit
}

func invalid(format string, args ...any) error {
	return util.NewInvalidArgumentErrorf(format, args...)
}

// Normalize trims and validates the input in place. Errors wrap util.ErrInvalidArgument and are safe to show.
func (in *Input) Normalize() error {
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)
	in.CVSSVector = strings.TrimSpace(in.CVSSVector)
	in.CVEID = strings.ToUpper(strings.TrimSpace(in.CVEID))
	in.Severity = strings.ToLower(strings.TrimSpace(in.Severity))

	if in.Title == "" || utf8.RuneCountInString(in.Title) > maxTitle {
		return invalid("Title is required and may have at most %d characters.", maxTitle)
	}
	if in.Description == "" || len(in.Description) > maxDescription {
		return invalid("Description is required and may have at most %d bytes.", maxDescription)
	}
	if in.CVSSVector != "" {
		score, err := cvss.Score(in.CVSSVector)
		if err != nil {
			return invalid("CVSS vector: %v.", err)
		}
		in.Severity = severityFromScore(score)
	}
	if in.Severity == "medium" {
		in.Severity = "moderate"
	}
	if !slices.Contains(Severities, in.Severity) {
		return invalid("Severity must be one of low, moderate, high or critical.")
	}
	if in.CVEID != "" && !cveRe.MatchString(in.CVEID) {
		return invalid("CVE id must look like CVE-2026-12345.")
	}

	cwes := make([]string, 0, len(in.CWEIDs))
	for _, c := range in.CWEIDs {
		c = strings.ToUpper(strings.TrimSpace(c))
		if c == "" || slices.Contains(cwes, c) {
			continue
		}
		if !cweRe.MatchString(c) {
			return invalid("CWE %q must look like CWE-79.", c)
		}
		cwes = append(cwes, c)
	}
	in.CWEIDs = cwes

	if len(in.Affected) > maxList || len(in.CWEIDs) > maxList || len(in.Credits) > maxList {
		return invalid("At most %d affected products, CWEs and credits each.", maxList)
	}
	for i := range in.Affected {
		p := &in.Affected[i]
		p.Ecosystem = strings.ToLower(strings.TrimSpace(p.Ecosystem))
		p.Package = strings.TrimSpace(p.Package)
		p.VulnerableRange = strings.TrimSpace(p.VulnerableRange)
		p.Patched = strings.TrimSpace(p.Patched)
		if _, ok := Ecosystems[p.Ecosystem]; !ok {
			return invalid("Unknown ecosystem %q.", p.Ecosystem)
		}
		if p.Package == "" || len(p.Package) > maxTitle || !packageRe.MatchString(p.Package) {
			return invalid("Package name %q is invalid.", p.Package)
		}
		if _, err := ParseRange(p.VulnerableRange); err != nil {
			return invalid("Vulnerable versions of %s: %v.", p.Package, err)
		}
		if p.Patched != "" && !versionRe.MatchString(p.Patched) {
			return invalid("Patched version of %s must be a single version like 1.4.2.", p.Package)
		}
	}

	seen := map[int64]bool{}
	credits := in.Credits[:0]
	for _, c := range in.Credits {
		c.Type = strings.ToLower(strings.TrimSpace(c.Type))
		if c.Type == "" {
			c.Type = "finder"
		}
		if !slices.Contains(CreditTypes, c.Type) {
			return invalid("Unknown credit type %q.", c.Type)
		}
		if c.UserID <= 0 || seen[c.UserID] {
			continue
		}
		seen[c.UserID] = true
		credits = append(credits, c)
	}
	in.Credits = credits
	return nil
}

func severityFromScore(score float64) string {
	switch s := cvss.Severity(score); s {
	case "none":
		return "low"
	case "medium":
		return "moderate"
	default:
		return s
	}
}

// Range is a vulnerable version range in OSV terms. Introduced is "0" when there is no lower bound.
type Range struct {
	Introduced   string
	Fixed        string // exclusive upper bound ("< x")
	LastAffected string // inclusive upper bound ("<= x")
	Exact        string // "= x": only this version
}

// ParseRange parses GitHub's vulnerable version range syntax: "", "= 1.2", ">= 1.0", "< 2.0", "<= 2.0" or a lower
// and an upper bound separated by a comma (">= 1.0, < 1.4.2"). ">" and "!=" are rejected because OSV ranges
// cannot express them.
func ParseRange(s string) (Range, error) {
	r := Range{Introduced: "0"}
	if strings.TrimSpace(s) == "" {
		return r, nil // all versions
	}
	var lower, upper bool
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		var op, v string
		for _, o := range []string{">=", "<=", "!=", ">", "<", "="} {
			if strings.HasPrefix(part, o) {
				op, v = o, strings.TrimSpace(part[len(o):])
				break
			}
		}
		if op == "" {
			op, v = "=", part
		}
		if !versionRe.MatchString(v) {
			return r, fmt.Errorf("%q is not a version", v)
		}
		switch {
		case op == "=" && len(strings.Split(s, ",")) == 1:
			r.Exact = v
		case op == ">=" && !lower:
			lower, r.Introduced = true, v
		case op == "<" && !upper:
			upper, r.Fixed = true, v
		case op == "<=" && !upper:
			upper, r.LastAffected = true, v
		default:
			return r, fmt.Errorf("use \"= x\" alone, or at most one of \">= x\" and one of \"< y\" or \"<= y\"")
		}
	}
	return r, nil
}
