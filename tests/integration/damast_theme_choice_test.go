// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package integration

import (
	"net/http"
	"testing"

	auth_model "forgejo.org/models/auth"
	damast_model "forgejo.org/models/damast"
	"forgejo.org/models/db"
	"forgejo.org/models/unittest"
	user_model "forgejo.org/models/user"
	api "forgejo.org/modules/structs"
	"forgejo.org/tests"

	"github.com/stretchr/testify/assert"

	"github.com/stretchr/testify/require"
)

// unittest's bean helpers order by an id column, which this table has not
func damastHasThemeChoice(t *testing.T, userID int64) bool {
	t.Helper()
	has, err := db.GetEngine(db.DefaultContext).ID(userID).Exist(&damast_model.UserThemeChoice{})
	require.NoError(t, err)
	return has
}

func damastStoreTheme(t *testing.T, name, theme string) {
	t.Helper()
	u := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: name})
	u.Theme = theme
	require.NoError(t, user_model.UpdateUserCols(db.DefaultContext, u, "theme"))
}

// Accounts from before Damast have forgejo-auto stored, because Forgejo saves the default theme with every
// account. They see Damast's green theme until they pick forgejo-auto again.
func TestDamastThemeChoice(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	t.Run("stored forgejo-auto shows damast-auto", func(t *testing.T) {
		damastStoreTheme(t, "user2", "forgejo-auto")
		testSelectedTheme(t, loginUser(t, "user2"), "damast-auto", "Damast (follow system theme)", "")
		u := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "user2"})
		require.Equal(t, "forgejo-auto", u.Theme, "the stored theme is not changed")
	})

	t.Run("picking forgejo-auto again sticks", func(t *testing.T) {
		user := loginUser(t, "user2")
		testChangeTheme(t, user, "forgejo-auto")
		testSelectedTheme(t, user, "forgejo-auto", "Forgejo (follow system theme)", "")
		u := unittest.AssertExistsAndLoadBean(t, &user_model.User{Name: "user2"})
		require.True(t, damastHasThemeChoice(t, u.ID))

		// another theme and back: still respected
		testChangeTheme(t, user, "damast-dark")
		testSelectedTheme(t, user, "damast-dark", "Damast dark", "")
		testChangeTheme(t, user, "forgejo-auto")
		testSelectedTheme(t, user, "forgejo-auto", "Forgejo (follow system theme)", "")
	})

	t.Run("other Forgejo themes stay", func(t *testing.T) {
		damastStoreTheme(t, "user4", "forgejo-dark")
		testSelectedTheme(t, loginUser(t, "user4"), "forgejo-dark", "Forgejo dark", "")
		damastStoreTheme(t, "user4", "forgejo-light-tritanopia")
		testSelectedTheme(t, loginUser(t, "user4"), "forgejo-light-tritanopia", "forgejo-light-tritanopia", "")
	})

	t.Run("API reports and records the theme like the web UI", func(t *testing.T) {
		damastStoreTheme(t, "user5", "forgejo-auto")
		session := loginUser(t, "user5")
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteUser)
		var settings api.UserSettings
		DecodeJSON(t, MakeRequest(t, NewRequest(t, "GET", "/api/v1/user/settings").AddTokenAuth(token), http.StatusOK), &settings)
		assert.Equal(t, "damast-auto", settings.Theme)

		theme := "forgejo-auto"
		req := NewRequestWithJSON(t, "PATCH", "/api/v1/user/settings", &api.UserSettingsOptions{Theme: &theme}).AddTokenAuth(token)
		DecodeJSON(t, MakeRequest(t, req, http.StatusOK), &settings)
		assert.Equal(t, "forgejo-auto", settings.Theme)
		testSelectedTheme(t, session, "forgejo-auto", "Forgejo (follow system theme)", "")
	})

	t.Run("deleting the user deletes the choice", func(t *testing.T) {
		u := &user_model.User{Name: "damast-theme-user", Email: "damast-theme-user@example.com", Passwd: "password", Type: user_model.UserTypeIndividual}
		deleteUser := createUser(t.Context(), t, u)
		require.NoError(t, db.Insert(db.DefaultContext, &damast_model.UserThemeChoice{UserID: u.ID}))
		require.True(t, damastHasThemeChoice(t, u.ID))
		deleteUser()
		require.False(t, damastHasThemeChoice(t, u.ID))
	})
}
