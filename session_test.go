// Copyright (C) 2025-2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package goapi

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// resetSessions clears the global session map so tests do not leak into each
// other.
func resetSessions(t *testing.T) {
	t.Helper()
	activeSessionsLock.Lock()
	activeSessions = make(map[string]*Session)
	activeSessionsLock.Unlock()
}

// backdate forces a session to look idle by setting its lastOp into the past.
func backdate(s *Session, d time.Duration) {
	s.innerLock.Lock()
	s.lastOp = time.Now().Add(-d)
	s.innerLock.Unlock()
}

func TestActiveSessionsCountsOnlyAlive(t *testing.T) {
	resetSessions(t)

	if n := ActiveSessions(); n != 0 {
		t.Fatalf("empty registry: %d", n)
	}

	a, _, _ := newSession("")
	newSession("")
	if n := ActiveSessions(); n != 2 {
		t.Fatalf("two fresh sessions: %d", n)
	}

	backdate(a, SessionTTL+time.Second)
	if n := ActiveSessions(); n != 1 {
		t.Errorf("one expired, not yet reaped: %d", n)
	}

	reapSessions()
	if n := ActiveSessions(); n != 1 {
		t.Errorf("after reaping: %d", n)
	}
}

func TestNewSessionReuseWhenValid(t *testing.T) {
	resetSessions(t)

	s, created, err := newSession("")
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	if !created {
		t.Fatalf("first call should create a session")
	}
	s.SetUser("alice")
	s.Set("k", "v")

	s2, created2, err := newSession(s.id)
	if err != nil {
		t.Fatalf("newSession(id): %v", err)
	}
	if created2 {
		t.Errorf("valid session should be reused, got created=true")
	}
	if s2 != s {
		t.Errorf("expected the same session instance to be reused")
	}
	if s2.User() != "alice" || s2.Get("k") != "v" {
		t.Errorf("reused session lost its state: user=%q k=%v", s2.User(), s2.Get("k"))
	}
}

func TestNewSessionExpiryOnAccess(t *testing.T) {
	resetSessions(t)

	s, _, err := newSession("")
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	id := s.id
	s.SetUser("alice")
	s.Set("k", "v")

	backdate(s, SessionTTL+time.Minute) // idle past the TTL

	s2, created, err := newSession(id)
	if err != nil {
		t.Fatalf("newSession(id): %v", err)
	}
	if !created {
		t.Errorf("expired session should be recreated (created=true)")
	}
	if s2.User() != "" {
		t.Errorf("fresh session should have empty user, got %q", s2.User())
	}
	if s2.Get("k") != nil {
		t.Errorf("fresh session should have empty data, got %v", s2.Get("k"))
	}
}

func TestReapSessions(t *testing.T) {
	resetSessions(t)

	stale := &Session{id: "stale", innerLock: &sync.RWMutex{}, data: map[string]interface{}{}}
	fresh := &Session{id: "fresh", innerLock: &sync.RWMutex{}, data: map[string]interface{}{}}
	backdate(stale, SessionTTL+time.Minute)
	backdate(fresh, 0)

	activeSessionsLock.Lock()
	activeSessions["stale"] = stale
	activeSessions["fresh"] = fresh
	activeSessionsLock.Unlock()

	reapSessions()

	activeSessionsLock.RLock()
	_, staleExists := activeSessions["stale"]
	_, freshExists := activeSessions["fresh"]
	activeSessionsLock.RUnlock()

	if staleExists {
		t.Errorf("stale session should have been reaped")
	}
	if !freshExists {
		t.Errorf("fresh session should have been retained")
	}
}

// TestSessionConcurrency exercises the session paths together under -race.
func TestSessionConcurrency(t *testing.T) {
	resetSessions(t)

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, _, err := newSession("")
			if err != nil {
				return
			}
			s.Set("k", 1)
			_ = s.Get("k")
			s.SetUser("u")
			_ = s.User()
			reapSessions()
		}()
	}
	wg.Wait()
}

func TestSessionDumpRoundTrip(t *testing.T) {
	resetSessions(t)
	path := filepath.Join(t.TempDir(), "sessions.json")

	s, _, _ := newSession("")
	s.SetUser("alice")
	s.Set("k", "v")
	s.innerLock.Lock()
	s.host, s.userAgent = "10.0.0.7:4242", "TestAgent/1.0"
	s.innerLock.Unlock()

	if err := SessionDump(path); err != nil {
		t.Fatalf("SessionDump: %v", err)
	}

	resetSessions(t)
	if err := RestoreSessions(path); err != nil {
		t.Fatalf("RestoreSessions: %v", err)
	}

	r, created, _ := newSession(s.id)
	if created || r.User() != "alice" || r.Get("k") != "v" || r.host != "10.0.0.7:4242" || r.userAgent != "TestAgent/1.0" {
		t.Errorf("restored session differs: created=%v user=%q k=%v host=%q ua=%q", created, r.User(), r.Get("k"), r.host, r.userAgent)
	}
}

func TestRestoreSessionsOldDumpFormat(t *testing.T) {
	resetSessions(t)
	path := filepath.Join(t.TempDir(), "sessions.json")
	old := `{"abc":{"id":"abc","data":{"k":1},"lastOp":"` + time.Now().Format(time.RFC3339Nano) + `","userName":"bob"}}`
	os.WriteFile(path, []byte(old), 0o600)

	if err := RestoreSessions(path); err != nil {
		t.Fatalf("RestoreSessions: %v", err)
	}
	r, created, _ := newSession("abc")
	if created || r.User() != "bob" || r.Get("k") != 1.0 {
		t.Errorf("old dump not restored: created=%v user=%q", created, r.User())
	}
	r.Set("x", 1) // a dump with "data": null must still yield a usable map
}
