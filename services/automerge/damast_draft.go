// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package automerge

import (
	"context"

	"forgejo.org/models/db"
	issues_model "forgejo.org/models/issues"
	pull_model "forgejo.org/models/pull"
	user_model "forgejo.org/models/user"
	"forgejo.org/modules/log"
	notify_service "forgejo.org/services/notify"
)

// DAMAST: like on GitHub, converting a pull request to a draft cancels its scheduled auto merge,
// so marking it ready later doesn't merge it without anyone asking again.
type damastDraftNotifier struct {
	notify_service.NullNotifier
}

func init() {
	notify_service.RegisterNotifier(&damastDraftNotifier{})
}

func (damastDraftNotifier) IssueChangeTitle(ctx context.Context, doer *user_model.User, issue *issues_model.Issue, oldTitle string) {
	if !issue.IsPull || issues_model.HasWorkInProgressPrefix(oldTitle) || !issues_model.HasWorkInProgressPrefix(issue.Title) {
		return
	}
	if err := issue.LoadPullRequest(ctx); err != nil {
		log.Error("LoadPullRequest: %v", err)
		return
	}
	exist, _, err := pull_model.GetScheduledMergeByPullID(ctx, issue.PullRequest.ID)
	if err != nil {
		log.Error("GetScheduledMergeByPullID: %v", err)
	}
	if !exist {
		return
	}
	if err := db.WithTx(ctx, func(ctx context.Context) error {
		if err := pull_model.DeleteScheduledAutoMerge(ctx, issue.PullRequest.ID); err != nil {
			return err
		}
		_, err := issues_model.CreateAutoMergeComment(ctx, issues_model.CommentTypePRUnScheduledToAutoMerge, issue.PullRequest, doer)
		return err
	}); err != nil {
		log.Error("cancel auto merge of draft %-v: %v", issue.PullRequest, err)
	}
}
