// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package security

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"forgejo.org/models/damast"
	repo_model "forgejo.org/models/repo"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/modules/util"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// osvSchema compiles testdata/osv-schema-1.6.7.json, the official OSV schema
// (https://github.com/ossf/osv-schema, validation/schema.json at v1.6.7, Apache-2.0). The only change: the id
// prefix list also allows DAMAST. That list holds the registered home databases; a self-hosted forge is its own
// database and cannot register, so everything else is checked as is.
func osvSchema(t *testing.T) *jsonschema.Schema {
	raw, err := os.ReadFile("testdata/osv-schema-1.6.7.json")
	require.NoError(t, err)
	patched := bytes.Replace(raw, []byte(`"^(ASB-A|`), []byte(`"^(DAMAST|ASB-A|`), 1)
	require.NotEqual(t, raw, patched, "prefix pattern not found")
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(patched))
	require.NoError(t, err)
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	require.NoError(t, c.AddResource("osv.json", doc))
	s, err := c.Compile("osv.json")
	require.NoError(t, err)
	return s
}

func validateOSV(t *testing.T, s *jsonschema.Schema, o *OSV) map[string]any {
	t.Helper()
	b, err := json.Marshal(o)
	require.NoError(t, err)
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	require.NoError(t, err)
	require.NoError(t, s.Validate(inst), string(b))
	var m map[string]any
	require.NoError(t, json.Unmarshal(b, &m))
	return m
}

func TestToOSV(t *testing.T) {
	defer test.MockVariableValue(&setting.AppURL, "https://git.example.com/")()
	schema := osvSchema(t)
	repo := &repo_model.Repository{ID: 1, OwnerName: "alice", Name: "tool"}

	full := &damast.SecurityAdvisory{
		RepoID: 1, AdvisoryID: "DAMAST-2345-6789-cfgh", State: damast.AdvisoryPublished,
		Title: "XSS in the renderer", Description: "Details in **markdown**.", Severity: "moderate",
		CVSSVector: "CVSS:3.1/AV:N/AC:L/PR:N/UI:R/S:C/C:L/I:L/A:N", CVEID: "CVE-2026-12345", CWEIDs: []string{"CWE-79"},
		Affected: []damast.AffectedProduct{
			{Ecosystem: "npm", Package: "@alice/tool", VulnerableRange: ">= 1.0.0, < 1.4.2", Patched: "1.4.2"},
			{Ecosystem: "pip", Package: "tool", VulnerableRange: "<= 2.0"},
			{Ecosystem: "go", Package: "example.com/tool", VulnerableRange: "= 0.3.1"},
			{Ecosystem: "rust", Package: "tool", VulnerableRange: "", Patched: "0.9.0"},
			{Ecosystem: "maven", Package: "com.example:tool", VulnerableRange: ""},
		},
		CreatedUnix: 1700000000, UpdatedUnix: 1700000500, PublishedUnix: 1700000400,
	}
	o, err := ToOSV(context.Background(), repo, full)
	require.NoError(t, err)
	m := validateOSV(t, schema, o)

	assert.Equal(t, "1.6.0", m["schema_version"])
	assert.Equal(t, "DAMAST-2345-6789-cfgh", m["id"])
	assert.Equal(t, "2023-11-14T22:20:00Z", m["published"])
	assert.Equal(t, "2023-11-14T22:21:40Z", m["modified"], "modified is the last edit")
	assert.Equal(t, []any{"CVE-2026-12345"}, m["aliases"])
	assert.Equal(t, []any{map[string]any{"type": "CVSS_V3", "score": full.CVSSVector}}, m["severity"])
	assert.Equal(t, map[string]any{"severity": "MODERATE", "cwe_ids": []any{"CWE-79"}, "repository": "alice/tool"}, m["database_specific"])

	require.Len(t, o.Affected, 5)
	assert.Equal(t, OSVPackage{Ecosystem: "npm", Name: "@alice/tool"}, o.Affected[0].Package)
	assert.Equal(t, []OSVEvent{{Introduced: "1.0.0"}, {Fixed: "1.4.2"}}, o.Affected[0].Ranges[0].Events)
	assert.Equal(t, "PyPI", o.Affected[1].Package.Ecosystem)
	assert.Equal(t, []OSVEvent{{Introduced: "0"}, {LastAffected: "2.0"}}, o.Affected[1].Ranges[0].Events)
	assert.Empty(t, o.Affected[2].Ranges)
	assert.Equal(t, []string{"0.3.1"}, o.Affected[2].Versions)
	assert.Equal(t, "crates.io", o.Affected[3].Package.Ecosystem)
	assert.Equal(t, []OSVEvent{{Introduced: "0"}, {Fixed: "0.9.0"}}, o.Affected[3].Ranges[0].Events)
	assert.Equal(t, []OSVEvent{{Introduced: "0"}}, o.Affected[4].Ranges[0].Events)
	assert.Equal(t, []OSVReference{
		{Type: "ADVISORY", URL: "https://git.example.com/alice/tool/security/advisories/DAMAST-2345-6789-cfgh"},
		{Type: "PACKAGE", URL: "https://git.example.com/alice/tool"},
	}, o.References)

	minimal := &damast.SecurityAdvisory{
		RepoID: 1, AdvisoryID: "DAMAST-wwww-xxxx-2222", State: damast.AdvisoryPublished, Title: "t", Description: "d",
		Severity: "low", UpdatedUnix: 1, PublishedUnix: 1,
	}
	o, err = ToOSV(context.Background(), repo, minimal)
	require.NoError(t, err)
	m = validateOSV(t, schema, o)
	assert.NotContains(t, m, "aliases")
	assert.NotContains(t, m, "severity")
	assert.NotContains(t, m, "affected")
	assert.NotContains(t, m, "withdrawn")

	withdrawn := *minimal
	withdrawn.State, withdrawn.WithdrawnUnix, withdrawn.UpdatedUnix = damast.AdvisoryWithdrawn, 1700000900, 1700000900
	o, err = ToOSV(context.Background(), repo, &withdrawn)
	require.NoError(t, err)
	m = validateOSV(t, schema, o)
	assert.Equal(t, "2023-11-14T22:28:20Z", m["withdrawn"])
	assert.Equal(t, m["withdrawn"], m["modified"])

	// every ecosystem we offer must be valid OSV
	for key := range Ecosystems {
		a := *minimal
		a.Affected = []damast.AffectedProduct{{Ecosystem: key, Package: "p"}}
		o, err := ToOSV(context.Background(), repo, &a)
		require.NoError(t, err)
		validateOSV(t, schema, o)
	}
}

func TestParseRange(t *testing.T) {
	for in, want := range map[string]Range{
		"":                 {Introduced: "0"},
		"< 1.2":            {Introduced: "0", Fixed: "1.2"},
		"<=1.2":            {Introduced: "0", LastAffected: "1.2"},
		">= 1.0":           {Introduced: "1.0"},
		">= 1.0, < 2.0":    {Introduced: "1.0", Fixed: "2.0"},
		"< 2.0, >= 1.0":    {Introduced: "1.0", Fixed: "2.0"},
		"= 1.0.0-beta.1":   {Introduced: "0", Exact: "1.0.0-beta.1"},
		"1.0.0":            {Introduced: "0", Exact: "1.0.0"},
		">= v1.0, <= v1.9": {Introduced: "v1.0", LastAffected: "v1.9"},
	} {
		got, err := ParseRange(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, in := range []string{"> 1.0", "!= 1.0", ">= 1, >= 2", "< 1, <= 2", "= 1, < 2", ">= ", "< 1 2", "<script>"} {
		_, err := ParseRange(in)
		assert.Error(t, err, in)
	}
}

func TestInputNormalize(t *testing.T) {
	valid := func() *Input {
		return &Input{Title: " Title ", Description: "d", Severity: "High", CWEIDs: []string{"cwe-79", " CWE-79", ""}}
	}
	in := valid()
	require.NoError(t, in.Normalize())
	assert.Equal(t, "Title", in.Title)
	assert.Equal(t, "high", in.Severity)
	assert.Equal(t, []string{"CWE-79"}, in.CWEIDs)

	in = valid()
	in.CVSSVector = "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"
	in.Severity = "low"
	require.NoError(t, in.Normalize())
	assert.Equal(t, "critical", in.Severity, "severity follows the CVSS score")

	in = valid()
	in.Severity = "medium"
	require.NoError(t, in.Normalize())
	assert.Equal(t, "moderate", in.Severity)

	in = valid()
	in.Credits = []damast.Credit{{UserID: 2}, {UserID: 2, Type: "analyst"}}
	require.NoError(t, in.Normalize())
	assert.Equal(t, []damast.Credit{{UserID: 2, Type: "finder"}}, in.Credits)

	for name, mutate := range map[string]func(*Input){
		"no title":       func(in *Input) { in.Title = " " },
		"no description": func(in *Input) { in.Description = "" },
		"bad severity":   func(in *Input) { in.Severity = "urgent" },
		"bad cvss":       func(in *Input) { in.CVSSVector = "CVSS:3.1/AV:N" },
		"bad cve":        func(in *Input) { in.CVEID = "CVE-1" },
		"bad cwe":        func(in *Input) { in.CWEIDs = []string{"79"} },
		"bad ecosystem":  func(in *Input) { in.Affected = []damast.AffectedProduct{{Ecosystem: "other", Package: "p"}} },
		"no package":     func(in *Input) { in.Affected = []damast.AffectedProduct{{Ecosystem: "npm"}} },
		"bad range": func(in *Input) {
			in.Affected = []damast.AffectedProduct{{Ecosystem: "npm", Package: "p", VulnerableRange: "> 1"}}
		},
		"bad patched": func(in *Input) {
			in.Affected = []damast.AffectedProduct{{Ecosystem: "npm", Package: "p", Patched: "1, 2"}}
		},
		"bad credit type": func(in *Input) { in.Credits = []damast.Credit{{UserID: 1, Type: "hero"}} },
	} {
		in := valid()
		mutate(in)
		err := in.Normalize()
		assert.True(t, errors.Is(err, util.ErrInvalidArgument), name)
	}
}

func TestNewAdvisoryID(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		id := damast.NewAdvisoryID()
		assert.Regexp(t, `^DAMAST-[2-9cfghjmpqrvwx]{4}-[2-9cfghjmpqrvwx]{4}-[2-9cfghjmpqrvwx]{4}$`, id)
		assert.False(t, seen[id])
		seen[id] = true
	}
}
