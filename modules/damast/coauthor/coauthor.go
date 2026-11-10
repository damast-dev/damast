// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package coauthor parses "Co-authored-by:" git trailers.
package coauthor

import (
	"strings"
	"unicode"
)

// Identity is a co-author as written in the trailer.
type Identity struct {
	Name  string
	Email string
}

const trailerKey = "co-authored-by:"

// maxCoAuthors bounds the per-commit work (one user lookup per co-author) and the rendered HTML.
const maxCoAuthors = 100

// unsafeRune reports control and bidi override characters, which could reorder or hide text around a name.
func unsafeRune(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Bidi_Control, r) }

// Parse returns the co-authors from the trailer block (last paragraph) of a
// commit message, deduplicated by email and without authorEmail.
func Parse(message, authorEmail string) []Identity {
	message = strings.TrimSpace(strings.ReplaceAll(message, "\r\n", "\n"))
	i := strings.LastIndex(message, "\n\n")
	if i < 0 {
		return nil // the first paragraph is the subject, never a trailer block (as in git interpret-trailers)
	}
	message = message[i+2:]

	seen := map[string]bool{strings.ToLower(strings.TrimSpace(authorEmail)): true}
	var out []Identity
	for _, line := range strings.Split(message, "\n") {
		line = strings.TrimSpace(line)
		if len(line) <= len(trailerKey) || !strings.EqualFold(line[:len(trailerKey)], trailerKey) {
			continue
		}
		// Split like git's ident parser ("Name <email>"), not RFC 5322: names like
		// "dependabot[bot]" or "Doe, John" are valid in git but rejected by net/mail.
		v := strings.TrimSpace(line[len(trailerKey):])
		lt := strings.LastIndex(v, "<")
		if lt < 0 || !strings.HasSuffix(v, ">") {
			continue
		}
		email := strings.TrimSpace(v[lt+1 : len(v)-1])
		at := strings.Index(email, "@")
		if at <= 0 || at != strings.LastIndex(email, "@") || at == len(email)-1 || strings.ContainsAny(email, "<> ") || strings.IndexFunc(email, unsafeRune) >= 0 {
			continue
		}
		key := strings.ToLower(email)
		if seen[key] {
			continue
		}
		seen[key] = true
		name := strings.Trim(strings.TrimSpace(strings.Map(func(r rune) rune {
			if unsafeRune(r) {
				return -1
			}
			return r
		}, v[:lt])), `"`)
		if name == "" {
			name = email
		}
		out = append(out, Identity{Name: name, Email: email})
		if len(out) == maxCoAuthors {
			break
		}
	}
	return out
}
