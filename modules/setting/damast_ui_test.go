// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDamastDefaultTheme(t *testing.T) {
	defaultTheme, themes := UI.DefaultTheme, UI.Themes
	for _, tt := range []struct {
		name, ini, want string
		wantThemes      []string
	}{
		{"no config", ``, "damast-auto", nil},
		{"explicit default", "[ui]\nDEFAULT_THEME = forgejo-dark", "forgejo-dark", nil},
		{"own list without damast", "[ui]\nTHEMES = forgejo-auto,gitea-auto", "forgejo-auto", []string{"forgejo-auto", "gitea-auto"}},
		{"own list with damast", "[ui]\nTHEMES = gitea-auto,damast-auto", "damast-auto", []string{"gitea-auto", "damast-auto"}},
		{"own list and default", "[ui]\nTHEMES = gitea-auto\nDEFAULT_THEME = gitea-auto", "gitea-auto", []string{"gitea-auto"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			UI.DefaultTheme, UI.Themes = defaultTheme, themes
			t.Cleanup(func() { UI.DefaultTheme, UI.Themes = defaultTheme, themes })
			cfg, err := NewConfigProviderFromData(tt.ini)
			require.NoError(t, err)
			loadUIFrom(cfg)
			assert.Equal(t, tt.want, UI.DefaultTheme)
			if tt.wantThemes != nil {
				assert.Equal(t, tt.wantThemes, UI.Themes)
			} else {
				assert.Equal(t, []string{"damast-auto", "damast-light", "damast-dark"}, UI.Themes[:3])
			}
		})
	}
}
