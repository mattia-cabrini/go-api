// Copyright (C) 2025 Mattia Cabrini
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

var activeSessionsLock = &sync.RWMutex{}
var activeSessions = make(map[string]*Session)

type Session struct {
	id       string
	userName string
	lastOp   time.Time

	innerLock *sync.RWMutex
	data      map[string]interface{}
}

func newSession(id string) (s *Session, err error) {
	defer utility.Monitor(activeSessionsLock)()

	var b = false

	if id == "" {
		for id, err = utility.RandString(24); err == nil; id, err = utility.RandString(24) {
			_, b := activeSessions[id]
			if !b { // not duplicated session id
				break
			}
		}
	}

	if err != nil {
		err = utility.AppendError(err)
		return
	}

	if s, b = activeSessions[id]; !b {
		s = &Session{
			id:        id,
			innerLock: &sync.RWMutex{},
			data:      make(map[string]interface{}),
		}
		activeSessions[id] = s
	}

	s.lastOp = time.Now()

	return
}

func (s *Session) User() string {
	defer utility.RMonitor(s.innerLock)()
	return s.userName
}

func (s *Session) SetUser(usr string) {
	defer utility.RMonitor(s.innerLock)()
	s.userName = usr
}

func (s *Session) Get(key string) (v interface{}) {
	defer utility.RMonitor(s.innerLock)()
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
	defer utility.Monitor(s.innerLock)()
	delete(activeSessions, s.id)
}

func (s *Session) GetCookie() *http.Cookie {
	return &http.Cookie{
		Name:     "sessionid",
		Value:    s.id,
		Secure:   true,
		Expires:  time.Now().Add(15 * time.Minute),
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

		mx["id"] = sx.id
		mx["data"] = sx.data
		mx["lastOp"] = sx.lastOp
		mx["userName"] = sx.userName

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
