// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package damast

import (
	"context"
	"slices"

	"forgejo.org/models/db"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/log"
	"forgejo.org/modules/setting"
	"forgejo.org/modules/timeutil"
)

const (
	forgejoAuto = "forgejo-auto"
	damastAuto  = "damast-auto"
)

// UserThemeChoice marks a user who picked forgejo-auto on Damast. Without a row, a stored forgejo-auto
// (Forgejo's default, saved with every account) is shown as damast-auto.
type UserThemeChoice struct {
	UserID     int64              `xorm:"pk"`
	ChosenUnix timeutil.TimeStamp `xorm:"created"`
}

// TableName keeps all Damast tables under the damast_ prefix.
func (UserThemeChoice) TableName() string { return "damast_user_theme_choice" }

func init() {
	db.RegisterModel(new(UserThemeChoice))
}

// ApplyTheme shows damast-auto to a user whose stored theme is forgejo-auto and who never picked it on
// Damast. It only changes the loaded user, never the stored theme, so Forgejo shows the stored one again.
func ApplyTheme(ctx context.Context, u *user_model.User) {
	if u == nil || u.Theme != forgejoAuto || !slices.Contains(setting.UI.Themes, damastAuto) {
		return
	}
	chosen, err := db.GetEngine(ctx).ID(u.ID).Exist(&UserThemeChoice{})
	if err != nil {
		log.Error("damast: theme choice of user %d: %v", u.ID, err)
		return
	}
	if !chosen {
		u.Theme = damastAuto
	}
}

// RecordThemeChoice remembers that a user picked forgejo-auto; any other theme drops that again.
func RecordThemeChoice(ctx context.Context, userID int64, theme string) error {
	e := db.GetEngine(ctx)
	if theme != forgejoAuto {
		_, err := e.Delete(&UserThemeChoice{UserID: userID})
		return err
	}
	if has, err := e.ID(userID).Exist(&UserThemeChoice{}); err != nil || has {
		return err
	}
	_, err := e.Insert(&UserThemeChoice{UserID: userID})
	return err
}
