// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package translation

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/modules/translation/i18n"
	"forgejo.org/modules/translation/localeiter"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var damastPlaceholder = regexp.MustCompile(`%(\[\d+\])?[-+# 0]*\d*(\.\d+)?[a-zA-Z]`)

func damastMessages(t *testing.T, path string) map[string]string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	msgs := map[string]string{}
	require.NoError(t, localeiter.IterateMessagesNextContent(data, func(key, form, value string) error {
		msgs[key+"#"+form] = value
		return nil
	}), path)
	return msgs
}

// Every Damast message file is valid, only uses damast.* keys that exist in en-US, keeps the
// placeholders of the English message, and belongs to a language Forgejo ships.
func TestDamastLocaleFiles(t *testing.T) {
	dir := "../../options/locale_damast"
	en := damastMessages(t, filepath.Join(dir, "locale_en-US.json"))
	require.NotEmpty(t, en)
	files, err := filepath.Glob(filepath.Join(dir, "locale_*.json"))
	require.NoError(t, err)
	for _, f := range files {
		lang := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "locale_"), ".json")
		t.Run(lang, func(t *testing.T) {
			_, err := os.Stat(filepath.Join("../../options/locale_next", "locale_"+lang+".json"))
			require.NoError(t, err, "Forgejo has no language %s", lang)
			for key, msg := range damastMessages(t, f) {
				// themes.names.<theme> is the name Forgejo looks up for a theme; Forgejo never has damast-* themes
				assert.True(t, strings.HasPrefix(key, "damast.") || strings.HasPrefix(key, "themes.names.damast-"),
					"key %s must start with damast. or themes.names.damast-", key)
				base, form, _ := strings.Cut(key, "#")
				ref, ok := en[key]
				if !ok && form != "" {
					ref, ok = en[base+"#other"] // languages have other plural forms than English
				}
				if !assert.True(t, ok, "key %s is not in en-US", key) {
					continue
				}
				want := damastPlaceholder.FindAllString(ref, -1)
				got := damastPlaceholder.FindAllString(msg, -1)
				slices.Sort(want)
				slices.Sort(got)
				assert.Equal(t, want, got, "placeholders of %s", key)
			}
		})
	}
}

// A language without a Damast translation shows English, even when the default language is not en-US.
func TestDamastLocaleFallback(t *testing.T) {
	defer test.MockVariableValue(&setting.StaticRootPath, "../..")()
	defer test.MockVariableValue(&setting.Langs, []string{"de-DE", "fr-FR", "en-US"})()
	defer test.MockVariableValue(&setting.Names, []string{"Deutsch", "Français", "English"})()
	defer test.MockVariableValue(&setting.IsProd, true)()
	t.Cleanup(func() { // other tests in this package expect no loaded locales
		i18n.ResetDefaultLocales()
		allLangs, allLangMap = nil, map[string]*LangType{}
	})
	InitLocales(t.Context())

	assert.Equal(t, "Repository-Zugriff", NewLocale("de-DE").TrString("damast.packages.repo_access"))
	assert.Equal(t, "Repository access", NewLocale("en-US").TrString("damast.packages.repo_access"))
	if _, err := os.Stat("../../options/locale_damast/locale_fr-FR.json"); os.IsNotExist(err) {
		assert.Equal(t, "Repository access", NewLocale("fr-FR").TrString("damast.packages.repo_access"))
	}
	assert.Equal(t, "2 weitere Personen", string(NewLocale("de-DE").TrN(2, "damast.coauthors.others", "damast.coauthors.others", 2)))
	assert.Equal(t, "Damast dunkel", NewLocale("de-DE").TrString("themes.names.damast-dark"))
}
