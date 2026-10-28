// Copyright (C) 2025-2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package goapi

import (
	"crypto/sha1"
	"encoding/hex"
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
	id        string
	userName  string
	lastOp    time.Time
	host      string // remote address the session was created from
	userAgent string // User-Agent of the request that created the session

	innerLock *sync.RWMutex
	data      map[string]interface{}
}

// sessionRecord is a Session as written in the session dump.
type sessionRecord struct {
	ID        string                 `json:"id"`
	Data      map[string]interface{} `json:"data"`
	LastOp    time.Time              `json:"lastOp"`
	UserName  string                 `json:"userName"`
	Host      string                 `json:"host"`
	UserAgent string                 `json:"userAgent"`
}

// newSession returns the session for id, creating a fresh one when id is empty,
// unknown, or references a session that has expired (idle longer than
// SessionTTL). created reports whether a brand-new session was minted, which the
// caller uses to treat expired/unknown cookies like a first-time visitor.
func newSession(id string) (s *Session, created bool, err error) {
	defer utility.Monitor(activeSessionsLock)()

	if existing, ok := activeSessions[id]; ok {
		if existing.alive() {
			existing.touch()
			return existing, false, nil
		}

		destroy(existing) // expired: destroy and recreate fresh
	}

	// Generate a non-colliding id when none was supplied.
	for id == "" || activeSessions[id] != nil {
		if id, err = utility.RandString(24); err != nil {
			return nil, false, utility.AppendError(err)
		}
	}

	s = &Session{
		id:        id,
		innerLock: &sync.RWMutex{},
		data:      make(map[string]interface{}),
		lastOp:    time.Now(),
	}
	activeSessions[id] = s

	return s, true, nil
}

// alive reports whether the session was used within SessionTTL.
func (s *Session) alive() bool {
	defer utility.RMonitor(s.innerLock)()
	return time.Since(s.lastOp) <= SessionTTL
}

// touch records a use of the session.
func (s *Session) touch() {
	defer utility.Monitor(s.innerLock)()
	s.lastOp = time.Now()
	s.logEvent("modified")
}

// destroy removes s from activeSessions. The caller must hold
// activeSessionsLock.
func destroy(s *Session) {
	unlock := utility.RMonitor(s.innerLock)
	s.logEvent("destroyed")
	unlock()

	delete(activeSessions, s.id)
}

// logEvent writes a VERBOSE line describing the session's origin. The token is
// logged as its SHA-1 digest: still unique per session, but unusable to hijack
// it from the logs. The caller must hold s.innerLock (read or write).
func (s *Session) logEvent(event string) {
	digest := sha1.Sum([]byte(s.id))
	utility.Logf(utility.VERBOSE, "session %s %s for %s@%s using %s", hex.EncodeToString(digest[:]), event, s.userName, s.host, s.userAgent)
}

// reapSessions destroys every session that has been idle longer than SessionTTL.
// Deleting map entries during a range is safe in Go.
func reapSessions() {
	defer utility.Monitor(activeSessionsLock)()

	for _, sx := range activeSessions {
		if !sx.alive() {
			destroy(sx)
		}
	}
}

// ActiveSessions returns how many sessions are alive: used within SessionTTL,
// whether or not the reaper has swept the expired ones yet.
func ActiveSessions() (n int) {
	defer utility.RMonitor(activeSessionsLock)()

	for _, sx := range activeSessions {
		if sx.alive() {
			n++
		}
	}

	return
}

func (s *Session) User() string {
	defer utility.RMonitor(s.innerLock)()
	return s.userName
}

func (s *Session) SetUser(usr string) {
	defer utility.Monitor(s.innerLock)()
	if s.userName != usr {
		s.userName = usr
		s.logEvent("modified")
	}
}

func (s *Session) Get(key string) interface{} {
	defer utility.Monitor(s.innerLock)()
	s.lastOp = time.Now()
	return s.data[key]
}

func (s *Session) Set(key string, v interface{}) {
	defer utility.Monitor(s.innerLock)()
	s.lastOp = time.Now()
	s.data[key] = v
}

func (s *Session) Delete() {
	defer utility.Monitor(activeSessionsLock)()
	destroy(s)
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

// record is a snapshot of the session for the dump. The data map is copied,
// otherwise the JSON encoder may iterate it while a request handler mutates it
// via Session.Set (concurrent map iteration/write).
func (s *Session) record() sessionRecord {
	defer utility.RMonitor(s.innerLock)()

	data := make(map[string]interface{}, len(s.data))
	for k, v := range s.data {
		data[k] = v
	}

	return sessionRecord{ID: s.id, Data: data, LastOp: s.lastOp, UserName: s.userName, Host: s.host, UserAgent: s.userAgent}
}

// SessionDump writes every active session to path as JSON.
func SessionDump(path string) error {
	defer utility.Monitor(activeSessionsLock)()

	records := make(map[string]sessionRecord, len(activeSessions))
	for id, sx := range activeSessions {
		records[id] = sx.record()
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err == nil {
		defer f.Close()
		err = json.NewEncoder(f).Encode(records)
	}

	return utility.AppendError(err)
}

// dumpSessions writes the session dump, logging a failure rather than
// returning it.
func dumpSessions(path string) {
	if err := SessionDump(path); err != nil {
		utility.Logf(utility.ERROR, "%v", err)
	}
}

// RestoreSessions loads the sessions SessionDump wrote to path, if any.
func RestoreSessions(path string) error {
	if path == "" {
		return nil
	}

	defer utility.Monitor(activeSessionsLock)()

	f, err := os.Open(path)
	if err != nil {
		return utility.AppendError(err)
	}
	defer f.Close()

	var records map[string]sessionRecord
	if err = json.NewDecoder(f).Decode(&records); err != nil {
		return utility.AppendError(err)
	}

	for _, rec := range records {
		if rec.Data == nil {
			rec.Data = make(map[string]interface{})
		}

		activeSessions[rec.ID] = &Session{
			id:        rec.ID,
			userName:  rec.UserName,
			lastOp:    rec.LastOp,
			host:      rec.Host,
			userAgent: rec.UserAgent,
			innerLock: &sync.RWMutex{},
			data:      rec.Data,
		}
	}

	return nil
}
