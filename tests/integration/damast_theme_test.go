// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"net/http"
	"testing"

	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"
)

func TestDamastTheme(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	t.Run("guests get damast-auto", func(t *testing.T) {
		page := NewHTMLParser(t, MakeRequest(t, NewRequest(t, "GET", "/user2/repo1"), http.StatusOK).Body)
		theme, _ := page.Find("html").Attr("data-theme")
		assert.Equal(t, "damast-auto", theme)
		page.AssertElement(t, "html head link[rel='stylesheet'][href^='/assets/css/theme-damast-auto.css']", true)
	})

	t.Run("theme names", func(t *testing.T) {
		user := loginUser(t, "user2")
		testChangeTheme(t, user, "damast-dark")
		testSelectedTheme(t, user, "damast-dark", "Damast dark", "")
		testChangeTheme(t, user, "damast-light")
		testSelectedTheme(t, user, "damast-light", "Damast light", "")
	})

	t.Run("Forgejo themes stay available", func(t *testing.T) {
		user := loginUser(t, "user2")
		testChangeTheme(t, user, "forgejo-auto")
		testSelectedTheme(t, user, "forgejo-auto", "Forgejo (follow system theme)", "")
		testChangeTheme(t, user, "forgejo-dark-deuteranopia-protanopia")
		testSelectedTheme(t, user, "forgejo-dark-deuteranopia-protanopia", "forgejo-dark-deuteranopia-protanopia", "")
	})

	// Going back to Forgejo: its theme list has no damast-* themes, and a user's damast-* theme falls back
	// to Forgejo's default theme when the user is loaded.
	t.Run("going back to Forgejo", func(t *testing.T) {
		user := loginUser(t, "user2")
		testChangeTheme(t, user, "damast-dark")
		defer test.MockVariableValue(&setting.UI.DefaultTheme, "forgejo-auto")()
		defer test.MockVariableValue(&setting.UI.Themes, setting.UI.Themes[3:])()
		testSelectedTheme(t, user, "forgejo-auto", "Forgejo (follow system theme)", "")
	})
}
