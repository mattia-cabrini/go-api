// Copyright (C) 2025 Mattia Cabrini
// SPDX-License-Identifier: MIT

package goapi

import (
	"sync"

	"github.com/mattia-cabrini/go-utility"
)

var chronoSerMutex = &sync.Mutex{}

func chronoSerialize(path string) {
	defer utility.Monitor(chronoSerMutex)()

	if err := SessionDump(path); err != nil {
		utility.Logf(utility.ERROR, "%v", err)
	}
}
