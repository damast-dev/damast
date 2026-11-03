// Copyright 2026 The Damast Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

// Package coauthor parses "Co-authored-by:" git trailers.
package coauthor

import (
	"net/mail"
	"strings"
)

// Identity is a co-author as written in the trailer.
type Identity struct {
	Name  string
	Email string
}

const trailerKey = "co-authored-by:"

// Parse returns the co-authors from the trailer block (last paragraph) of a
// commit message, deduplicated by email and without authorEmail.
func Parse(message, authorEmail string) []Identity {
	message = strings.TrimSpace(strings.ReplaceAll(message, "\r\n", "\n"))
	if i := strings.LastIndex(message, "\n\n"); i >= 0 {
		message = message[i+2:]
	} else if !strings.Contains(message, "\n") {
		return nil // a single-line message is a subject, never a trailer block
	}

	seen := map[string]bool{strings.ToLower(strings.TrimSpace(authorEmail)): true}
	var out []Identity
	for _, line := range strings.Split(message, "\n") {
		line = strings.TrimSpace(line)
		if len(line) <= len(trailerKey) || !strings.EqualFold(line[:len(trailerKey)], trailerKey) {
			continue
		}
		addr, err := mail.ParseAddress(strings.TrimSpace(line[len(trailerKey):]))
		if err != nil {
			continue
		}
		key := strings.ToLower(addr.Address)
		if seen[key] {
			continue
		}
		seen[key] = true
		name := addr.Name
		if name == "" {
			name = addr.Address
		}
		out = append(out, Identity{Name: name, Email: addr.Address})
	}
	return out
}
