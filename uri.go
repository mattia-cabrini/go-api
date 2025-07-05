// Copyright (C) 2025 Mattia Cabrini
// SPDX-License-Identifier: MIT

package goapi

import "strings"

type URI struct {
	comp []string
	path string

	stackIx int
}

func InitURI(uri string) (u URI) {
	u.path = strings.Split(uri, "?")[0]
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

func (u *URI) ResetStack() {
	u.stackIx = 0
}
