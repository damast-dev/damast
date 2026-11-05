// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package setting

import "slices"

// damastDefaultTheme keeps Forgejo's default theme for installs that list their own [ui] THEMES without
// Damast's themes and set no DEFAULT_THEME: they keep looking exactly as they did on Forgejo, instead of
// getting a default theme their users cannot pick in the settings.
func damastDefaultTheme(sec ConfigSection) {
	if !sec.HasKey("DEFAULT_THEME") && !slices.Contains(UI.Themes, UI.DefaultTheme) {
		UI.DefaultTheme = "forgejo-auto"
	}
}
