// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package packages

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRepoNameFromURL(t *testing.T) {
	app, _ := url.Parse("https://git.example.com/sub/")
	for in, want := range map[string]string{
		"https://git.example.com/sub/alice/tool":          "tool",
		"git+https://git.example.com/sub/Alice/Tool.git":  "tool",
		"https://git.example.com/sub/alice/tool/src/main": "tool",
		"https://git.example.com/sub/bob/tool":            "", // other owner
		"https://evil.example.org/sub/alice/tool":         "", // other host
		"https://git.example.com/sub/alice":               "",
		"not a url":                                       "",
	} {
		assert.Equal(t, want, repoNameFromURL(in, app, "alice"), in)
	}
}

func TestRepoNamesFromPackageName(t *testing.T) {
	assert.Equal(t, []string{"foo"}, repoNamesFromPackageName("foo"))
	assert.Equal(t, []string{"foo"}, repoNamesFromPackageName("foo/bar"))
	assert.Equal(t, []string{"foo"}, repoNamesFromPackageName("@scope/foo"))
}
