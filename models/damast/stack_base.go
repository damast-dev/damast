// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package damast

import (
	"context"

	"forgejo.org/models/db"
	"forgejo.org/modules/timeutil"
)

// StackBase remembers the head commit of the parent a stacked pull request was last based on.
// A rebase after the parent was squash-merged or rewritten needs it:
// `git rebase --onto <new base> <old parent head>` replays only the layer's own commits.
type StackBase struct {
	PullRequestID int64              `xorm:"pk"`
	ParentHead    string             `xorm:"VARCHAR(64) NOT NULL"`
	UpdatedUnix   timeutil.TimeStamp `xorm:"updated"`
}

// TableName keeps all Damast tables under the damast_ prefix.
func (StackBase) TableName() string { return "damast_stack_base" }

func init() {
	db.RegisterModel(new(StackBase))
}

// GetStackBase returns the recorded parent head of a pull request, "" when there is none.
func GetStackBase(ctx context.Context, pullRequestID int64) (string, error) {
	b := &StackBase{}
	has, err := db.GetEngine(ctx).ID(pullRequestID).Get(b)
	if !has {
		return "", err
	}
	return b.ParentHead, err
}

// SetStackBase creates or replaces the recorded parent head of a pull request.
func SetStackBase(ctx context.Context, pullRequestID int64, parentHead string) error {
	e := db.GetEngine(ctx)
	has, err := e.ID(pullRequestID).Exist(&StackBase{})
	if err != nil {
		return err
	}
	if has {
		_, err = e.ID(pullRequestID).Cols("parent_head").Update(&StackBase{ParentHead: parentHead})
		return err
	}
	_, err = e.Insert(&StackBase{PullRequestID: pullRequestID, ParentHead: parentHead})
	return err
}
