// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package translation

import (
	"forgejo.org/modules/log"
	"forgejo.org/modules/options"
	"forgejo.org/modules/translation/i18n"
)

// damastLocaleFile is Damast's own message file for a language, next to Forgejo's locale and locale_next.
func damastLocaleFile(lang string) string {
	return "locale_damast/locale_" + lang + ".json"
}

// damastAddLocale merges Damast's messages into a language. en-US goes in first, so a missing translation
// shows English even when the instance's default language is not en-US.
func damastAddLocale(lang string) {
	files := []string{"en-US"}
	if lang != "en-US" {
		files = append(files, lang)
	}
	for _, l := range files {
		bytes, err := options.AssetFS().ReadFile(damastLocaleFile(l))
		if err != nil {
			if l == "en-US" {
				log.Error("Failed to open Damast messages for en-US: %v", err)
			}
			continue // no translation yet: the en-US messages loaded above stay
		}
		if err = i18n.DefaultLocales.AddToLocaleFromJSON(lang, bytes); err != nil {
			log.Error("Failed to add Damast messages from %s to %s: %v", l, lang, err)
		}
	}
}
