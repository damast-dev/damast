// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package coauthor

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testdata/parse_vectors.json is shared with mast (src/damast/coauthors_vectors.json): keep both copies equal.
func TestParse(t *testing.T) {
	data, err := os.ReadFile("testdata/parse_vectors.json")
	require.NoError(t, err)
	var cases []struct {
		Name, Message, Author string
		Want                  [][2]string
	}
	require.NoError(t, json.Unmarshal(data, &cases))
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var want []Identity
			for _, w := range c.Want {
				want = append(want, Identity{w[0], w[1]})
			}
			assert.Equal(t, want, Parse(c.Message, c.Author))
		})
	}
}

func TestParseCap(t *testing.T) {
	var msg strings.Builder
	msg.WriteString("mob\n\n")
	for i := range 1000 {
		fmt.Fprintf(&msg, "Co-authored-by: N%d <n%d@x.org>\n", i, i)
	}
	got := Parse(msg.String(), "")
	assert.Len(t, got, maxCoAuthors)
	assert.Equal(t, Identity{"N99", "n99@x.org"}, got[99])
}
