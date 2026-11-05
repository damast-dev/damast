// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package draft

import (
	"testing"

	"forgejo.org/modules/setting"
	"forgejo.org/modules/test"

	"github.com/stretchr/testify/assert"
)

func TestDraftTitle(t *testing.T) {
	defer test.MockVariableValue(&setting.Repository.PullRequest.WorkInProgressPrefixes, []string{"WIP:", "[WIP]"})()

	assert.True(t, IsDraft("WIP: x"))
	assert.True(t, IsDraft("wip: x"))
	assert.True(t, IsDraft("[wip] x"))
	assert.False(t, IsDraft("x WIP:"))

	assert.Equal(t, "x", Strip("WIP: x"))
	assert.Equal(t, "x", Strip("[WIP]x"))
	assert.Equal(t, "WIP:", Strip("WIP:"))
	assert.Equal(t, "x", Strip("x"))

	assert.Equal(t, "WIP: x", Mark("x"))
	assert.Equal(t, "wip: x", Mark("wip: x"))
	assert.Equal(t, "[WIP] x", Mark("[WIP] x"))

	assert.Equal(t, "x", DisplayTitle("WIP: x", true))
	assert.Equal(t, "WIP: x", DisplayTitle("WIP: x", false), "issues keep their title")
	assert.True(t, Enabled())

	defer test.MockVariableValue(&setting.Repository.PullRequest.WorkInProgressPrefixes, []string{})()
	assert.Equal(t, "x", Mark("x"))
	assert.False(t, IsDraft("WIP: x"))
	assert.False(t, Enabled())
}
