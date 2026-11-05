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
	"forgejo.org/modules/translation"
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

// InputError is a validation error. Key is a locale key (options/locale_damast) so the web UI can show it in
// the user's language; Error() is English, for the API and logs.
type InputError struct {
	Key  string
	Args []any
}

func (e *InputError) Error() string { return translation.NewLocale("en-US").TrString(e.Key, e.Args...) }

func (e *InputError) Unwrap() error { return util.ErrInvalidArgument }

func invalid(key string, args ...any) error {
	return &InputError{Key: "damast.security.error." + key, Args: args}
}

// Normalize trims and validates the input in place. Errors wrap util.ErrInvalidArgument and are safe to show.
func (in *Input) Normalize() error {
	in.Title = strings.TrimSpace(in.Title)
	in.Description = strings.TrimSpace(in.Description)
	in.CVSSVector = strings.TrimSpace(in.CVSSVector)
	in.CVEID = strings.ToUpper(strings.TrimSpace(in.CVEID))
	in.Severity = strings.ToLower(strings.TrimSpace(in.Severity))

	if in.Title == "" || utf8.RuneCountInString(in.Title) > maxTitle {
		return invalid("title", maxTitle)
	}
	if in.Description == "" || len(in.Description) > maxDescription {
		return invalid("description", maxDescription)
	}
	if in.CVSSVector != "" {
		score, err := cvss.Score(in.CVSSVector)
		if err != nil { // the detail stays in English, the message says what a valid vector looks like
			return invalid("cvss")
		}
		in.Severity = severityFromScore(score)
	}
	if in.Severity == "medium" {
		in.Severity = "moderate"
	}
	if !slices.Contains(Severities, in.Severity) {
		return invalid("severity")
	}
	if in.CVEID != "" && !cveRe.MatchString(in.CVEID) {
		return invalid("cve")
	}

	cwes := make([]string, 0, len(in.CWEIDs))
	for _, c := range in.CWEIDs {
		c = strings.ToUpper(strings.TrimSpace(c))
		if c == "" || slices.Contains(cwes, c) {
			continue
		}
		if !cweRe.MatchString(c) {
			return invalid("cwe", c)
		}
		cwes = append(cwes, c)
	}
	in.CWEIDs = cwes

	if len(in.Affected) > maxList || len(in.CWEIDs) > maxList || len(in.Credits) > maxList {
		return invalid("too_many", maxList)
	}
	for i := range in.Affected {
		p := &in.Affected[i]
		p.Ecosystem = strings.ToLower(strings.TrimSpace(p.Ecosystem))
		p.Package = strings.TrimSpace(p.Package)
		p.VulnerableRange = strings.TrimSpace(p.VulnerableRange)
		p.Patched = strings.TrimSpace(p.Patched)
		if _, ok := Ecosystems[p.Ecosystem]; !ok {
			return invalid("ecosystem", p.Ecosystem)
		}
		if p.Package == "" || len(p.Package) > maxTitle || !packageRe.MatchString(p.Package) {
			return invalid("package", p.Package)
		}
		if _, err := ParseRange(p.VulnerableRange); err != nil {
			return invalid("range", p.Package)
		}
		if p.Patched != "" && !versionRe.MatchString(p.Patched) {
			return invalid("patched", p.Package)
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
			return invalid("credit_type", c.Type)
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

// CheckSeverity rejects an explicitly requested severity that a CVSS vector would silently override.
func CheckSeverity(severity, vector string) error {
	score, err := cvss.Score(strings.TrimSpace(vector))
	if err != nil {
		return nil // no or an invalid vector: Normalize decides
	}
	want := severityFromScore(score)
	if s := strings.ToLower(strings.TrimSpace(severity)); s != want && !(s == "medium" && want == "moderate") {
		return invalid("severity_cvss", want)
	}
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
