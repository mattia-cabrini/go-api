// Copyright (C) 2025-2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package goapi

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path"
	"syscall"
	"time"

	"github.com/mattia-cabrini/go-utility"
)

// startSession returns the session the request's cookie names, or a fresh one
// when there is no cookie or its session is unknown or expired; created reports
// the latter, so such requests are treated like a first-time visitor's.
func startSession(w http.ResponseWriter, r *http.Request) (s *Session, created bool, err error) {
	var id string
	if c, cerr := r.Cookie("sessionid"); cerr == nil {
		id = c.Value
	}

	if s, created, err = newSession(id); s == nil {
		return
	}

	http.SetCookie(w, s.GetCookie())

	if created {
		unlock := utility.Monitor(s.innerLock)
		s.host, s.userAgent = r.RemoteAddr, r.UserAgent()
		s.logEvent("created")
		unlock()
	}

	return
}

// fail logs err and answers 500.
func fail(w http.ResponseWriter, err error) {
	utility.Logf(utility.ERROR, "%v", err)
	w.WriteHeader(http.StatusInternalServerError)
}

func handleRequest(m *utility.Method, request string, hasAuth bool, w http.ResponseWriter, r *http.Request) {
	defer func() {
		if i := recover(); i != nil {
			utility.Logf(utility.ERROR, "%v", i)
		}
	}()

	s, created, err := startSession(w, r)
	if err != nil {
		fail(w, err)
		return
	}

	// A visitor with no session may only log in, and a protected controller
	// needs a logged-in user.
	if (created && request != "Login") || (hasAuth && s.User() == "") {
		InitRedirectResponse("/Login", http.StatusTemporaryRedirect).Write(w)
		return
	}

	var res []interface{}

	switch m.NumIn() {
	case 1:
		res, err = m.F(s)
	case 2:
		res, err = m.F(s, initPoliteRequest(r))
	default:
		err = fmt.Errorf("handler for %s has %d parameters", r.RequestURI, m.NumIn())
	}

	if err != nil {
		fail(w, err)
		return
	}

	resp, ok := res[0].(Response)
	if !ok {
		jr := InitJsonResponse()
		jr.Set("data", res[0])
		resp = jr
	}

	resp.Write(w)
}

// handleDist serves the file uri names under dist, or a directory's own
// index.html for a directory. A miss on a path with no extension is a
// client-side route, so it gets the SPA shell, dist/index.html; a miss on a
// path that looks like an asset still 404s, so broken script/style URLs are
// not masked by the shell.
func handleDist(dist string, uri URI, w http.ResponseWriter, r *http.Request) {
	file := path.Join(dist, uri.path)

	s, err := os.Stat(file)
	if err == nil && s.IsDir() {
		file = path.Join(file, "index.html")
		_, err = os.Stat(file)
	}

	if err != nil && path.Ext(uri.path) == "" {
		file = path.Join(dist, "index.html")
		_, err = os.Stat(file)
	}

	if err != nil {
		utility.Logf(utility.INFO, "not found `%s`", uri.path)
		http.NotFound(w, r)
		return
	}

	http.ServeFile(w, r, file)
}

func getHandler(root interface{}, dist string) http.HandlerFunc {
	if s, err := os.Stat(dist); err != nil {
		utility.Logf(utility.FATAL, "%v", utility.AppendError(fmt.Errorf("could not stat %s: %v", dist, err)))
	} else if !s.IsDir() {
		utility.Logf(utility.FATAL, "%v", utility.AppendError(fmt.Errorf("%s is not a directory", dist)))
	}

	return func(w http.ResponseWriter, r *http.Request) {
		uri := InitURI(r.RequestURI)
		utility.Logf(utility.INFO, "URI: %s", r.RequestURI)

		// Walk the controller tree along the path; a controller tagged "auth"
		// protects everything below it.
		controller, hasAuth := root, false

		for uri.StackCount() > 1 && controller != nil {
			name := uri.Pop()

			if auth := utility.GetProperty(controller, name, "", "controller", "auth"); auth != nil {
				controller, hasAuth = auth, true
			} else {
				controller = utility.GetProperty(controller, name, "", "controller")
			}
		}

		var f *utility.Method
		var request string

		if controller != nil {
			if request = uri.Pop(); request != "" {
				f = utility.GetMethod(controller, request, "Request")
			}
		}

		if f != nil {
			handleRequest(f, request, hasAuth, w, r)
		} else {
			handleDist(dist, uri, w, r) // no handler: search in dist
		}
	}
}

// every runs f every d, forever.
func every(d time.Duration, f func()) {
	for {
		time.Sleep(d)
		f()
	}
}

func Run(rootController interface{}, dist string, bind string, cert string, key string, sessionDumpPath string) {
	http.HandleFunc("/", relay(getHandler(rootController, dist)))

	if err := RestoreSessions(sessionDumpPath); err != nil {
		utility.Logf(utility.ERROR, "could not restore sessions: %v", err)
	}

	go every(sessionReapInterval, reapSessions)

	if sessionDumpPath != "" {
		go every(time.Second, func() { dumpSessions(sessionDumpPath) })
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)

	go func() {
		utility.Logf(utility.INFO, "signal %v: exiting", <-sigs)

		if sessionDumpPath != "" {
			dumpSessions(sessionDumpPath)
		}

		os.Exit(0)
	}()

	if err := http.ListenAndServeTLS(bind, cert, key, nil); err != nil {
		utility.Mypanic(err)
	}
}
