// Copyright (C) 2025-2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package goapi

import "strings"

// URI is a request path split in its components, read as a stack: Pop yields
// them from the first, StackCount tells how many are left.
type URI struct {
	comp []string
	path string

	stackIx int
}

func InitURI(uri string) (u URI) {
	u.path, _, _ = strings.Cut(uri, "?")
	u.comp = strings.Split(u.path, "/")[1:]
	return
}

func (u *URI) Pop() (part string) {
	if u.stackIx < len(u.comp) {
		part = u.comp[u.stackIx]
		u.stackIx++
	}

	return
}

func (u *URI) StackCount() int {
	return len(u.comp) - u.stackIx
}
