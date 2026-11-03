// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package coauthor

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name, msg, author string
		want              []Identity
	}{
		{"none", "fix: thing\n\nbody text", "a@x.org", nil},
		{"subject only", "Co-authored-by: Bob <bob@x.org>", "a@x.org", nil},
		{
			"multiple, case-insensitive key, CRLF",
			"feat: x\r\n\r\nbody\r\n\r\nCo-authored-by: Bob <bob@x.org>\r\nco-AUTHORED-by: Eve Doe <eve@x.org>\r\nSigned-off-by: A <a@x.org>",
			"a@x.org",
			[]Identity{{"Bob", "bob@x.org"}, {"Eve Doe", "eve@x.org"}},
		},
		{
			"dedupe and drop author",
			"feat\n\nCo-authored-by: Bob <bob@x.org>\nCo-authored-by: bob <BOB@x.org>\nCo-authored-by: Me <A@x.org>",
			"a@x.org",
			[]Identity{{"Bob", "bob@x.org"}},
		},
		{"broken lines skipped", "feat\n\nCo-authored-by: no email\nCo-authored-by:\nCo-authored-by: <only@x.org>", "", []Identity{{"only@x.org", "only@x.org"}}},
		{"not in last paragraph", "feat\n\nCo-authored-by: Bob <bob@x.org>\n\nmore text", "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, Parse(c.msg, c.author))
		})
	}
}
