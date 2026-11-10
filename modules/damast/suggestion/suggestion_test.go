// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package suggestion

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name  string
		md    string
		want  []string
		found bool
		err   error
	}{
		{"none", "looks good\n```go\nx := 1\n```", nil, false, nil},
		{"single", "Rename:\n```suggestion\nfoo := 1\n```\nthanks", []string{"foo := 1"}, true, nil},
		{"empty block deletes", "```suggestion\n```", []string{}, true, nil},
		{"one empty line", "```suggestion\n\n```", []string{""}, true, nil},
		{"tilde fence", "~~~suggestion\na\nb\n~~~", []string{"a", "b"}, true, nil},
		{"longer fence with nested backticks", "````suggestion\n```go\nx\n```\n````", []string{"```go", "x", "```"}, true, nil},
		{"short closing fence is content", "````suggestion\na\n```\n````", []string{"a", "```"}, true, nil},
		{"tilde does not close backtick", "```suggestion\n~~~\n```", []string{"~~~"}, true, nil},
		{"CRLF", "x\r\n```suggestion\r\na\r\nb\r\n```\r\n", []string{"a", "b"}, true, nil},
		{"indented fence strips indent", "  ```suggestion\n    a\n  b\n  ```", []string{"  a", "b"}, true, nil},
		{"info string with extra words", "``` suggestion please\nz\n```", []string{"z"}, true, nil},
		{"four spaces is not a fence", "    ```suggestion\n    a\n    ```", nil, false, nil},
		{"suggestion inside other fence ignored", "````md\n```suggestion\na\n```\n````", nil, false, nil},
		{"unclosed runs to end", "```suggestion\na\nb", []string{"a", "b"}, true, nil},
		{"unicode", "```suggestion\nGrüße 👋 \t\n```", []string{"Grüße 👋 \t"}, true, nil},
		{"multiple rejected", "```suggestion\na\n```\n```suggestion\nb\n```", nil, true, ErrMultiple},
		{"other language", "```suggestions\na\n```", nil, false, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, found, err := Parse(c.md)
			assert.Equal(t, c.err, err)
			assert.Equal(t, c.found, found)
			assert.Equal(t, c.want, got)
		})
	}
}

func TestFence(t *testing.T) {
	assert.Equal(t, "```suggestion\na\n```", Fence([]string{"a"}))
	assert.Equal(t, "```suggestion\n```", Fence(nil))
	assert.Equal(t, "````suggestion\n```go\n````", Fence([]string{"```go"}))
	for _, lines := range [][]string{{"a", "", "b"}, {"```", "````x"}, {}} {
		got, found, err := Parse(Fence(lines))
		require.NoError(t, err)
		assert.True(t, found)
		assert.Equal(t, lines, got)
	}
}

func TestLines(t *testing.T) {
	got, err := Lines([]byte("a\r\nb\r\nc"), 2, 2)
	require.NoError(t, err)
	assert.Equal(t, []string{"b", "c"}, got)
	_, err = Lines([]byte("a\n"), 1, 2)
	assert.ErrorIs(t, err, ErrOutOfRange)
	_, err = Lines(nil, 1, 1)
	assert.ErrorIs(t, err, ErrOutOfRange)
}

func TestReplace(t *testing.T) {
	cases := []struct {
		name, in string
		edits    []Edit
		want     string
		err      error
	}{
		{"single line", "a\nb\nc\n", []Edit{{2, 1, []string{"B"}}}, "a\nB\nc\n", nil},
		{"range to more lines", "a\nb\nc\nd\n", []Edit{{2, 2, []string{"x", "y", "z"}}}, "a\nx\ny\nz\nd\n", nil},
		{"delete", "a\nb\nc\n", []Edit{{2, 1, nil}}, "a\nc\n", nil},
		{"delete everything", "a\nb\n", []Edit{{1, 2, nil}}, "", nil},
		{"no final newline kept", "a\nb", []Edit{{2, 1, []string{"B", "C"}}}, "a\nB\nC", nil},
		{"no final newline delete last", "a\nb", []Edit{{2, 1, nil}}, "a", nil},
		{"CRLF", "a\r\nb\r\nc\r\n", []Edit{{2, 1, []string{"x", "y"}}}, "a\r\nx\r\ny\r\nc\r\n", nil},
		{"CRLF no final newline", "a\r\nb", []Edit{{2, 1, []string{"B"}}}, "a\r\nB", nil},
		{"unicode", "ä\nö\nü\n", []Edit{{2, 1, []string{"Ö 👋"}}}, "ä\nÖ 👋\nü\n", nil},
		{"several, unsorted", "1\n2\n3\n4\n5\n", []Edit{{4, 1, []string{"four"}}, {1, 2, []string{"one"}}}, "one\n3\nfour\n5\n", nil},
		{"adjacent ok", "1\n2\n", []Edit{{1, 1, []string{"a"}}, {2, 1, []string{"b"}}}, "a\nb\n", nil},
		{"overlap", "1\n2\n3\n", []Edit{{1, 2, nil}, {2, 1, nil}}, "", ErrOverlap},
		{"out of range", "1\n", []Edit{{1, 2, nil}}, "", ErrOutOfRange},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Replace([]byte(c.in), c.edits)
			if c.err != nil {
				assert.ErrorIs(t, err, c.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, c.want, string(got))
		})
	}
}
