// Copyright (C) 2025-2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package goapi

import (
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/mattia-cabrini/go-utility"
)

// SessionTTL is the idle lifetime of a session. After this much inactivity a
// session is destroyed server-side; it is also used for the cookie's expiry so
// the client and server agree on the window.
const SessionTTL = 15 * time.Minute

// sessionReapInterval is how often the background reaper sweeps activeSessions
// for idle sessions to destroy.
const sessionReapInterval = 1 * time.Minute

var activeSessionsLock = &sync.RWMutex{}
var activeSessions = make(map[string]*Session)

type Session struct {
	id       string
	userName string
	lastOp   time.Time

	innerLock *sync.RWMutex
	data      map[string]interface{}
}

// newSession returns the session for id, creating a fresh one when id is empty,
// unknown, or references a session that has expired (idle longer than
// SessionTTL). created reports whether a brand-new session was minted, which the
// caller uses to treat expired/unknown cookies like a first-time visitor.
func newSession(id string) (s *Session, created bool, err error) {
	defer utility.Monitor(activeSessionsLock)()

	// Reuse an existing, still-valid session; destroy it on access if expired.
	if id != "" {
		if existing, ok := activeSessions[id]; ok {
			unlock := utility.Monitor(existing.innerLock)
			if time.Since(existing.lastOp) <= SessionTTL {
				existing.lastOp = time.Now()
				s = existing
			} else {
				delete(activeSessions, id) // expired -> destroy and recreate fresh
			}
			unlock()
		}
	}

	// Mint a fresh session when there is no valid one to reuse.
	if s == nil {
		// Generate a non-colliding id when none was supplied.
		if id == "" {
			for id, err = utility.RandString(24); err == nil; id, err = utility.RandString(24) {
				if _, ok := activeSessions[id]; !ok { // not a duplicated session id
					break
				}
			}
		}

		if err != nil {
			err = utility.AppendError(err)
		} else {
			s = &Session{
				id:        id,
				innerLock: &sync.RWMutex{},
				data:      make(map[string]interface{}),
				lastOp:    time.Now(),
			}
			activeSessions[id] = s
			created = true
		}
	}

	return
}

// reapSessions destroys every session that has been idle longer than SessionTTL.
// Deleting map entries during a range is safe in Go.
func reapSessions() {
	defer utility.Monitor(activeSessionsLock)()

	for id, sx := range activeSessions {
		unlock := utility.RMonitor(sx.innerLock)
		expired := time.Since(sx.lastOp) > SessionTTL
		unlock()

		if expired {
			delete(activeSessions, id)
		}
	}
}

// sessionReaper periodically reaps idle sessions. It is meant to run in its own
// goroutine for the lifetime of the process.
func sessionReaper() {
	for {
		time.Sleep(sessionReapInterval)
		reapSessions()
	}
}

func (s *Session) User() string {
	defer utility.RMonitor(s.innerLock)()
	return s.userName
}

func (s *Session) SetUser(usr string) {
	defer utility.Monitor(s.innerLock)()
	s.userName = usr
}

func (s *Session) Get(key string) (v interface{}) {
	defer utility.Monitor(s.innerLock)()
	s.lastOp = time.Now()
	v, b := s.data[key]

	if !b {
		v = nil
	}

	return v
}

func (s *Session) Set(key string, v interface{}) {
	defer utility.Monitor(s.innerLock)()
	s.lastOp = time.Now()
	s.data[key] = v
}

func (s *Session) Delete() {
	defer utility.Monitor(activeSessionsLock)()
	delete(activeSessions, s.id)
}

func (s *Session) GetCookie() *http.Cookie {
	return &http.Cookie{
		Name:     "sessionid",
		Value:    s.id,
		Secure:   true,
		Expires:  time.Now().Add(SessionTTL),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Path:     "/",
	}
}

func SessionDump(path string) error {
	defer utility.Monitor(activeSessionsLock)()

	var m = make(map[string]interface{})

	for _, sx := range activeSessions {
		var mx = make(map[string]interface{})

		// Read per-session state under its own lock and copy the data map,
		// otherwise the JSON encoder may iterate sx.data while a request
		// handler mutates it via Session.Set (concurrent map iteration/write).
		unlock := utility.RMonitor(sx.innerLock)
		dataCopy := make(map[string]interface{}, len(sx.data))
		for k, v := range sx.data {
			dataCopy[k] = v
		}
		mx["id"] = sx.id
		mx["data"] = dataCopy
		mx["lastOp"] = sx.lastOp
		mx["userName"] = sx.userName
		unlock()

		m[sx.id] = mx
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err == nil {
		enc := json.NewEncoder(f)
		err = enc.Encode(m)
	}

	return utility.AppendError(err)
}

func RestoreSessions(sessionDumpPath string) error {
	defer utility.Monitor(activeSessionsLock)()

	if sessionDumpPath == "" {
		return nil
	}

	var m = make(map[string]interface{})

	f, err := os.OpenFile(sessionDumpPath, os.O_RDONLY, 0600)
	if err == nil {
		dec := json.NewDecoder(f)
		dec.Decode(&m)

		for _, mxi := range m {
			var mx = mxi.(map[string]interface{})

			tm, _ := time.Parse(time.RFC3339Nano, mx["lastOp"].(string))
			var sx = &Session{
				id:        mx["id"].(string),
				data:      mx["data"].(map[string]interface{}),
				lastOp:    tm,
				userName:  mx["userName"].(string),
				innerLock: &sync.RWMutex{},
			}

			activeSessions[sx.id] = sx
		}
	}

	return utility.AppendError(err)
}
