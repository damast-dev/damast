// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package security

import (
	"context"
	"strings"
	"time"

	"forgejo.org/models/damast"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/modules/timeutil"
)

// OSVSchemaVersion is the OSV schema version the documents follow (https://ossf.github.io/osv-schema/).
const OSVSchemaVersion = "1.6.0"

// OSV is an advisory in the Open Source Vulnerability format.
type OSV struct {
	SchemaVersion    string         `json:"schema_version"`
	ID               string         `json:"id"`
	Modified         string         `json:"modified"`
	Published        string         `json:"published,omitempty"`
	Aliases          []string       `json:"aliases,omitempty"`
	Summary          string         `json:"summary,omitempty"`
	Details          string         `json:"details,omitempty"`
	Severity         []OSVSeverity  `json:"severity,omitempty"`
	Affected         []OSVAffected  `json:"affected,omitempty"`
	References       []OSVReference `json:"references"`
	Credits          []OSVCredit    `json:"credits,omitempty"`
	DatabaseSpecific map[string]any `json:"database_specific"`
}

type OSVSeverity struct {
	Type  string `json:"type"`
	Score string `json:"score"`
}

type OSVPackage struct {
	Ecosystem string `json:"ecosystem"`
	Name      string `json:"name"`
}

type OSVEvent struct {
	Introduced   string `json:"introduced,omitempty"`
	Fixed        string `json:"fixed,omitempty"`
	LastAffected string `json:"last_affected,omitempty"`
}

type OSVRange struct {
	Type   string     `json:"type"`
	Events []OSVEvent `json:"events"`
}

type OSVAffected struct {
	Package          OSVPackage     `json:"package"`
	Ranges           []OSVRange     `json:"ranges,omitempty"`
	Versions         []string       `json:"versions,omitempty"`
	DatabaseSpecific map[string]any `json:"database_specific,omitempty"`
}

type OSVReference struct {
	Type string `json:"type"`
	URL  string `json:"url"`
}

type OSVCredit struct {
	Name    string   `json:"name"`
	Contact []string `json:"contact,omitempty"`
	Type    string   `json:"type,omitempty"`
}

func osvTime(t timeutil.TimeStamp) string {
	return time.Unix(int64(t), 0).UTC().Format(time.RFC3339)
}

// ToOSV converts a published advisory. Callers must only pass published advisories.
func ToOSV(ctx context.Context, repo *repo_model.Repository, a *damast.SecurityAdvisory) (*OSV, error) {
	o := &OSV{
		SchemaVersion: OSVSchemaVersion,
		ID:            a.AdvisoryID,
		Modified:      osvTime(max(a.UpdatedUnix, a.PublishedUnix)),
		Summary:       a.Title,
		Details:       a.Description,
		References: []OSVReference{
			{Type: "ADVISORY", URL: HTMLURL(repo, a)},
			{Type: "PACKAGE", URL: repo.HTMLURL()},
		},
		DatabaseSpecific: map[string]any{
			"severity":   strings.ToUpper(a.Severity),
			"cwe_ids":    append([]string{}, a.CWEIDs...),
			"repository": repo.FullName(),
		},
	}
	if a.PublishedUnix > 0 {
		o.Published = osvTime(a.PublishedUnix)
	}
	if a.CVEID != "" {
		o.Aliases = []string{a.CVEID}
	}
	if a.CVSSVector != "" {
		o.Severity = []OSVSeverity{{Type: "CVSS_V3", Score: a.CVSSVector}}
	}
	for _, p := range a.Affected {
		r, err := ParseRange(p.VulnerableRange)
		if err != nil {
			return nil, err // validated on save, so this means a corrupt row
		}
		af := OSVAffected{
			Package:          OSVPackage{Ecosystem: Ecosystems[p.Ecosystem], Name: p.Package},
			DatabaseSpecific: map[string]any{"vulnerable_version_range": p.VulnerableRange, "patched_versions": p.Patched},
		}
		if r.Exact != "" {
			af.Versions = []string{r.Exact}
		} else {
			events := []OSVEvent{{Introduced: r.Introduced}}
			switch {
			case r.Fixed != "":
				events = append(events, OSVEvent{Fixed: r.Fixed})
			case r.LastAffected != "":
				events = append(events, OSVEvent{LastAffected: r.LastAffected})
			case p.Patched != "":
				events = append(events, OSVEvent{Fixed: p.Patched})
			}
			af.Ranges = []OSVRange{{Type: "ECOSYSTEM", Events: events}}
		}
		o.Affected = append(o.Affected, af)
	}
	if len(a.Credits) > 0 {
		ids := make([]int64, 0, len(a.Credits))
		for _, c := range a.Credits {
			ids = append(ids, c.UserID)
		}
		users, err := LoadUsers(ctx, ids...)
		if err != nil {
			return nil, err
		}
		for _, c := range a.Credits {
			if u := users[c.UserID]; !u.IsGhost() {
				o.Credits = append(o.Credits, OSVCredit{Name: u.GetDisplayName(), Contact: []string{u.HTMLURL()}, Type: strings.ToUpper(c.Type)})
			}
		}
	}
	return o, nil
}
