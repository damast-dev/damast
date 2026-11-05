// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package suggestion parses ```suggestion blocks from review comments and
// applies them to file contents.
//
// A suggestion block is a top-level fenced code block (``` or ~~~, three or
// more fence characters, indented by at most three spaces) whose info string
// starts with "suggestion". Its lines replace the commented lines on the
// proposed side; an empty block deletes them. A comment with more than one
// suggestion block can't be applied (ErrMultiple): it's ambiguous which lines
// each block replaces. Blocks nested in lists or quotes are ignored.
package suggestion

import (
	"errors"
	"sort"
	"strings"
)

// DefaultMessage is the commit message GitHub uses for applied suggestions.
const DefaultMessage = "Apply suggestions from code review"

var (
	// ErrMultiple is returned when a comment holds more than one suggestion block.
	ErrMultiple = errors.New("more than one suggestion block")
	// ErrOverlap is returned when two edits touch the same line.
	ErrOverlap = errors.New("suggestions overlap")
	// ErrOutOfRange is returned when an edit targets lines the file doesn't have.
	ErrOutOfRange = errors.New("lines out of range")
)

// Parse returns the lines of the suggestion block in markdown. found is false
// when there is none.
func Parse(markdown string) (lines []string, found bool, err error) {
	var (
		inFence    bool
		fenceChar  byte
		fenceLen   int
		indent     int
		suggestion bool
		blocks     int
		cur        []string
	)
	for _, line := range strings.Split(strings.ReplaceAll(markdown, "\r\n", "\n"), "\n") {
		if !inFence {
			ind, ch, n, info, ok := openingFence(line)
			if !ok {
				continue
			}
			inFence, fenceChar, fenceLen, indent = true, ch, n, ind
			suggestion = strings.SplitN(info, " ", 2)[0] == "suggestion"
			if suggestion {
				blocks++
				cur = []string{}
			}
			continue
		}
		if isClosingFence(line, fenceChar, fenceLen) {
			inFence = false
			if suggestion && blocks == 1 {
				lines = cur
			}
			continue
		}
		if suggestion {
			cur = append(cur, stripIndent(line, indent))
		}
	}
	if inFence && suggestion && blocks == 1 {
		lines = cur // an unclosed fence runs to the end of the document (CommonMark)
	}
	if blocks > 1 {
		return nil, true, ErrMultiple
	}
	return lines, blocks == 1, nil
}

func leadingSpaces(line string) int {
	n := 0
	for n < len(line) && line[n] == ' ' {
		n++
	}
	return n
}

func openingFence(line string) (indent int, ch byte, n int, info string, ok bool) {
	indent = leadingSpaces(line)
	if indent > 3 || indent >= len(line) || (line[indent] != '`' && line[indent] != '~') {
		return 0, 0, 0, "", false
	}
	ch = line[indent]
	for indent+n < len(line) && line[indent+n] == ch {
		n++
	}
	if n < 3 {
		return 0, 0, 0, "", false
	}
	info = strings.TrimSpace(line[indent+n:])
	if ch == '`' && strings.ContainsRune(info, '`') {
		return 0, 0, 0, "", false
	}
	return indent, ch, n, info, true
}

func isClosingFence(line string, ch byte, minLen int) bool {
	i := leadingSpaces(line)
	if i > 3 {
		return false
	}
	n := 0
	for i+n < len(line) && line[i+n] == ch {
		n++
	}
	return n >= minLen && strings.TrimSpace(line[i+n:]) == ""
}

func stripIndent(line string, indent int) string {
	n := min(leadingSpaces(line), indent)
	return line[n:]
}

// Fence returns a suggestion block for lines whose fence is longer than any
// backtick run inside, so code containing ``` survives.
func Fence(lines []string) string {
	fence := "```"
	for _, l := range lines {
		for strings.Contains(l, fence) {
			fence += "`"
		}
	}
	body := strings.Join(lines, "\n")
	if len(lines) > 0 {
		body += "\n"
	}
	return fence + "suggestion\n" + body + fence
}

// Edit replaces Count lines starting at line Start (1-based) with Lines.
type Edit struct {
	Start, Count int
	Lines        []string
}

// splitLines splits content into raw lines; a CR of a CRLF ending stays on its line.
func splitLines(content []byte) (lines []string, finalNewline bool) {
	s := string(content)
	if s == "" {
		return nil, false
	}
	lines = strings.Split(s, "\n")
	finalNewline = strings.HasSuffix(s, "\n")
	if finalNewline {
		lines = lines[:len(lines)-1]
	}
	return lines, finalNewline
}

// Lines returns count lines starting at start (1-based), without line endings.
func Lines(content []byte, start, count int) ([]string, error) {
	lines, _ := splitLines(content)
	if start < 1 || count < 1 || start+count-1 > len(lines) {
		return nil, ErrOutOfRange
	}
	out := make([]string, count)
	for i := range out {
		out[i] = strings.TrimSuffix(lines[start-1+i], "\r")
	}
	return out, nil
}

// Replace applies edits to content. New lines get the file's line ending
// (CRLF if its first line ends in CRLF), and a missing final newline stays missing.
func Replace(content []byte, edits []Edit) ([]byte, error) {
	lines, finalNewline := splitLines(content)
	cr := len(lines) > 0 && strings.HasSuffix(lines[0], "\r")

	edits = append([]Edit(nil), edits...)
	sort.Slice(edits, func(i, j int) bool { return edits[i].Start < edits[j].Start })
	for i, e := range edits {
		if e.Start < 1 || e.Count < 1 || e.Start+e.Count-1 > len(lines) {
			return nil, ErrOutOfRange
		}
		if i > 0 && edits[i-1].Start+edits[i-1].Count > e.Start {
			return nil, ErrOverlap
		}
	}
	for i := len(edits) - 1; i >= 0; i-- {
		e := edits[i]
		repl := make([]string, len(e.Lines))
		for j, l := range e.Lines {
			if cr {
				l += "\r"
			}
			repl[j] = l
		}
		lines = append(lines[:e.Start-1], append(repl, lines[e.Start-1+e.Count:]...)...)
	}

	out := strings.Join(lines, "\n")
	if len(lines) == 0 {
		return []byte{}, nil
	}
	if finalNewline {
		out += "\n"
	} else {
		out = strings.TrimSuffix(out, "\r")
	}
	return []byte(out), nil
}
