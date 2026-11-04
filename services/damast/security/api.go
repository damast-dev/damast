// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package security

import (
	"context"
	"time"

	"forgejo.org/models/damast"
	repo_model "forgejo.org/models/repo"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/damast/cvss"
	api "forgejo.org/modules/structs"
	"forgejo.org/modules/timeutil"
	"forgejo.org/modules/util"
	"forgejo.org/services/convert"
)

// API types follow GitHub's REST API for repository security advisories where it makes sense.

type APIPackage struct {
	Ecosystem string `json:"ecosystem"`
	Name      string `json:"name"`
}

type APIVulnerability struct {
	Package                APIPackage `json:"package"`
	VulnerableVersionRange string     `json:"vulnerable_version_range"`
	PatchedVersions        string     `json:"patched_versions"`
}

type APICVSS struct {
	VectorString string  `json:"vector_string"`
	Score        float64 `json:"score"`
}

type APIIdentifier struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type APICredit struct {
	Login string `json:"login"`
	Type  string `json:"type"`
}

// APIAdvisory is a repository security advisory.
type APIAdvisory struct {
	GhsaID          string             `json:"ghsa_id"` // GitHub's field name; holds the DAMAST-... id
	CveID           *string            `json:"cve_id"`
	URL             string             `json:"url"`
	HTMLURL         string             `json:"html_url"`
	OSVURL          string             `json:"osv_url,omitempty"`
	Summary         string             `json:"summary"`
	Description     string             `json:"description"`
	Severity        string             `json:"severity"` // low, medium, high, critical (GitHub's names)
	State           string             `json:"state"`
	Identifiers     []APIIdentifier    `json:"identifiers"`
	CreatedAt       time.Time          `json:"created_at"`
	UpdatedAt       time.Time          `json:"updated_at"`
	PublishedAt     *time.Time         `json:"published_at"`
	ClosedAt        *time.Time         `json:"closed_at"`
	Vulnerabilities []APIVulnerability `json:"vulnerabilities"`
	CVSS            *APICVSS           `json:"cvss"`
	CWEIDs          []string           `json:"cwe_ids"`
	Credits         []APICredit        `json:"credits"`
	// Only shown to people who can see the private discussion (reporter, collaborators, admins):
	Author             *api.User   `json:"author,omitempty"`
	CollaboratingUsers []*api.User `json:"collaborating_users,omitempty"`
}

// APIVulnerabilityOption is one affected product in a create or update request.
type APIVulnerabilityOption struct {
	Package                APIPackage `json:"package"`
	VulnerableVersionRange string     `json:"vulnerable_version_range"`
	PatchedVersions        string     `json:"patched_versions"`
}

// APIReportOption is the body of POST /security-advisories/reports.
type APIReportOption struct {
	Summary          string                   `json:"summary"`
	Description      string                   `json:"description"`
	Severity         string                   `json:"severity"`
	CVSSVectorString string                   `json:"cvss_vector_string"`
	CWEIDs           []string                 `json:"cwe_ids"`
	Vulnerabilities  []APIVulnerabilityOption `json:"vulnerabilities"`
}

// APICreateOption is the body of POST /security-advisories.
type APICreateOption struct {
	APIReportOption
	CveID   string      `json:"cve_id"`
	Credits []APICredit `json:"credits"`
}

// APIUpdateOption is the body of PATCH /security-advisories/{id}. Missing fields stay unchanged.
type APIUpdateOption struct {
	Summary            *string                   `json:"summary"`
	Description        *string                   `json:"description"`
	Severity           *string                   `json:"severity"`
	CVSSVectorString   *string                   `json:"cvss_vector_string"`
	CveID              *string                   `json:"cve_id"`
	CWEIDs             *[]string                 `json:"cwe_ids"`
	Vulnerabilities    *[]APIVulnerabilityOption `json:"vulnerabilities"`
	Credits            *[]APICredit              `json:"credits"`
	State              *string                   `json:"state"`
	CollaboratingUsers *[]string                 `json:"collaborating_users"`
}

// APIComment is a comment of the private discussion.
type APIComment struct {
	ID        int64     `json:"id"`
	Body      string    `json:"body"`
	User      *api.User `json:"user"`
	CreatedAt time.Time `json:"created_at"`
}

// APICommentOption is the body of POST .../comments.
type APICommentOption struct {
	Body string `json:"body"`
}

func apiTime(t timeutil.TimeStamp) *time.Time {
	if t == 0 {
		return nil
	}
	tt := t.AsTime()
	return &tt
}

func apiSeverity(s string) string {
	if s == "moderate" {
		return "medium"
	}
	return s
}

// ToAPI converts an advisory for a doer with the given level (at least Viewer).
func ToAPI(ctx context.Context, repo *repo_model.Repository, a *damast.SecurityAdvisory, doer *user_model.User, level Level) (*APIAdvisory, error) {
	apiURL := repo.APIURL() + "/security-advisories/" + a.AdvisoryID
	r := &APIAdvisory{
		GhsaID:      a.AdvisoryID,
		URL:         apiURL,
		HTMLURL:     HTMLURL(repo, a),
		Summary:     a.Title,
		Description: a.Description,
		Severity:    apiSeverity(a.Severity),
		State:       a.State,
		Identifiers: []APIIdentifier{{Type: "DAMAST", Value: a.AdvisoryID}},
		CreatedAt:   a.CreatedUnix.AsTime(),
		UpdatedAt:   a.UpdatedUnix.AsTime(),
		PublishedAt: apiTime(a.PublishedUnix),
		ClosedAt:    apiTime(a.ClosedUnix),
		CWEIDs:      append([]string{}, a.CWEIDs...),
		Credits:     []APICredit{},
	}
	if a.IsPublished() {
		r.OSVURL = apiURL + "/osv"
	}
	if a.CVEID != "" {
		r.CveID = &a.CVEID
		r.Identifiers = append(r.Identifiers, APIIdentifier{Type: "CVE", Value: a.CVEID})
	}
	if a.CVSSVector != "" {
		score, _ := cvss.Score(a.CVSSVector)
		r.CVSS = &APICVSS{VectorString: a.CVSSVector, Score: score}
	}
	r.Vulnerabilities = make([]APIVulnerability, 0, len(a.Affected))
	for _, p := range a.Affected {
		r.Vulnerabilities = append(r.Vulnerabilities, APIVulnerability{
			Package: APIPackage{Ecosystem: p.Ecosystem, Name: p.Package}, VulnerableVersionRange: p.VulnerableRange, PatchedVersions: p.Patched,
		})
	}
	ids := []int64{a.CreatorID}
	for _, c := range a.Credits {
		ids = append(ids, c.UserID)
	}
	var collaborators []int64
	if level >= Participant {
		var err error
		if collaborators, err = damast.AdvisoryCollaboratorIDs(ctx, a.ID); err != nil {
			return nil, err
		}
		ids = append(ids, collaborators...)
	}
	users, err := LoadUsers(ctx, ids...)
	if err != nil {
		return nil, err
	}
	for _, c := range a.Credits {
		if u := users[c.UserID]; !u.IsGhost() {
			r.Credits = append(r.Credits, APICredit{Login: u.Name, Type: c.Type})
		}
	}
	if level >= Participant {
		r.Author = convert.ToUser(ctx, users[a.CreatorID], doer)
		r.CollaboratingUsers = make([]*api.User, 0, len(collaborators))
		for _, id := range collaborators {
			r.CollaboratingUsers = append(r.CollaboratingUsers, convert.ToUser(ctx, users[id], doer))
		}
	}
	return r, nil
}

// ToAPIComment converts a comment.
func ToAPIComment(ctx context.Context, c *damast.AdvisoryComment, poster, doer *user_model.User) *APIComment {
	return &APIComment{ID: c.ID, Body: c.Content, User: convert.ToUser(ctx, poster, doer), CreatedAt: c.CreatedUnix.AsTime()}
}

// InputFromAPI builds an Input from an API request. Credit logins are resolved to users.
func InputFromAPI(ctx context.Context, o *APICreateOption) (*Input, error) {
	in := &Input{
		Title: o.Summary, Description: o.Description, Severity: o.Severity, CVSSVector: o.CVSSVectorString,
		CVEID: o.CveID, CWEIDs: o.CWEIDs,
	}
	for _, v := range o.Vulnerabilities {
		in.Affected = append(in.Affected, damast.AffectedProduct{
			Ecosystem: v.Package.Ecosystem, Package: v.Package.Name, VulnerableRange: v.VulnerableVersionRange, Patched: v.PatchedVersions,
		})
	}
	for _, c := range o.Credits {
		u, err := user_model.GetUserByName(ctx, c.Login)
		if err != nil {
			if user_model.IsErrUserNotExist(err) {
				return nil, util.NewInvalidArgumentErrorf("Unknown user %q in credits.", c.Login)
			}
			return nil, err
		}
		in.Credits = append(in.Credits, damast.Credit{UserID: u.ID, Type: c.Type})
	}
	return in, nil
}

// OptionFromAdvisory returns the current content of an advisory as a create option, the base for PATCH.
func OptionFromAdvisory(ctx context.Context, a *damast.SecurityAdvisory) (*APICreateOption, error) {
	o := &APICreateOption{
		APIReportOption: APIReportOption{
			Summary: a.Title, Description: a.Description, Severity: a.Severity, CVSSVectorString: a.CVSSVector, CWEIDs: a.CWEIDs,
		},
		CveID: a.CVEID,
	}
	for _, p := range a.Affected {
		o.Vulnerabilities = append(o.Vulnerabilities, APIVulnerabilityOption{
			Package: APIPackage{Ecosystem: p.Ecosystem, Name: p.Package}, VulnerableVersionRange: p.VulnerableRange, PatchedVersions: p.Patched,
		})
	}
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
			o.Credits = append(o.Credits, APICredit{Login: u.Name, Type: c.Type})
		}
	}
	return o, nil
}
