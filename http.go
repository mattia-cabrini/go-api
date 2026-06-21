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

func startSession(w http.ResponseWriter, r *http.Request) (s *Session, b bool, err error) {
	var id string
	if c, cerr := r.Cookie("sessionid"); cerr == nil {
		id = c.Value
	}

	// b reports a freshly minted session: no cookie, or a cookie whose session is
	// unknown or expired. Such requests are treated like a first-time visitor.
	s, b, err = newSession(id)

	if s != nil {
		http.SetCookie(w, s.GetCookie())
	}

	return
}

func handleRequest(m *utility.Method, request string, hasAuth bool, w http.ResponseWriter, r *http.Request) {
	var res []interface{}
	var err error

	defer func() {
		if i := recover(); i != nil {
			utility.Logf(utility.ERROR, "%v", i)
		}
	}()

	if m == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// utility.Logf(utility.INFO, "session start")
	s, newSession, err := startSession(w, r)

	if err != nil {
		utility.Logf(utility.ERROR, "%v\n", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if s == nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if newSession && request != "Login" {
		w.Header().Set("Location", "/Login")
		w.WriteHeader(http.StatusTemporaryRedirect)
		return
	}

	if hasAuth && s.User() == "" {
		w.Header().Set("Location", "/Login")
		w.WriteHeader(http.StatusTemporaryRedirect)
		return
	}

	switch politeRequest := initPoliteRequest(r); m.NumIn() {
	case 1:
		res, err = m.F(s)
	case 2:
		res, err = m.F(s, politeRequest)
	default:
		utility.Logf(utility.ERROR, "handler for %s has %d parameters\n", r.RequestURI, m.NumIn())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if err != nil {
		utility.Logf(utility.ERROR, "%v\n", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	respi := res[0]

	var resp Response
	var ok bool

	if resp, ok = respi.(Response); !ok {
		jr := InitJsonResponse()
		jr.Set("data", respi)
		resp = jr
	}

	resp.Write(w)
}

func handleDist(dist string, uri URI, w http.ResponseWriter, r *http.Request) {
	uri.ResetStack()

	err := handleFile(dist, &uri, w, r)

	if err != nil {
		// SPA fallback: a path with no file extension is a client-side route, not
		// an asset, so serve the SPA shell and let the router resolve it. Paths
		// that look like assets (have an extension) still 404 so broken
		// script/style URLs are not masked by index.html.
		served := false
		if path.Ext(uri.path) == "" {
			served = serveSPAIndex(dist, w, r) == nil
		}

		if !served {
			utility.Logf(utility.INFO, "not found `%s`", uri.path)
			http.NotFound(w, r)
		}
	}
}

// serveSPAIndex serves dist/index.html (the SPA shell). It returns an error
// without writing a response when index.html does not exist, so the caller can
// fall back to a 404.
func serveSPAIndex(dist string, w http.ResponseWriter, r *http.Request) (err error) {
	index := dist + "/index.html"

	if _, err = os.Stat(index); err == nil {
		http.ServeFile(w, r, index)
	}

	return
}

func handleFile(filePath string, uri *URI, w http.ResponseWriter, r *http.Request) (err error) {
	var part = ""

	if uri != nil {
		part = uri.Pop()
	}

	if part != "" {
		// Depth first: descend into the next path component. A miss deeper in the
		// tree propagates rather than being masked by this directory's
		// index.html, so genuinely missing assets can still 404 (the SPA fallback
		// in handleDist decides what to do with the miss).
		err = handleFile(filePath+"/"+part, uri, w, r)
	} else {
		// Leaf of the descent: serve the file, or fall back to the directory's
		// own index.html for a bare directory request.
		var s os.FileInfo

		if s, err = os.Stat(filePath); err == nil {
			if s.IsDir() {
				err = handleFile(filePath+"/"+"index.html", nil, w, r)
			} else {
				http.ServeFile(w, r, filePath)
			}
		}
	}

	return
}

func getHandler(controller interface{}, dist string) func(http.ResponseWriter, *http.Request) {
	s, err := os.Stat(dist)

	if err != nil {
		err = fmt.Errorf("could not stat %s: %v", dist, err)
		utility.Logf(utility.FATAL, "%v", utility.AppendError(err))
	}

	if !s.IsDir() {
		err = fmt.Errorf("%s is not a directory", dist)
		utility.Logf(utility.FATAL, "%v", utility.AppendError(err))
	}

	return func(w http.ResponseWriter, r *http.Request) {
		var f *utility.Method
		var request string
		var hasAuth = false

		controller := controller
		uri := InitURI(r.RequestURI)

		utility.Logf(utility.INFO, "URI: %s", r.RequestURI)

		for uri.StackCount() > 1 && controller != nil {
			controllerName := uri.Pop()
			controllerAuth := utility.GetProperty(controller, controllerName, "", "controller", "auth")

			if controllerAuth != nil {
				hasAuth = true
				controller = controllerAuth
			} else {
				controller = utility.GetProperty(controller, controllerName, "", "controller")
			}
		}

		if controller != nil {
			request = uri.Pop()

			if request != "" {
				f = utility.GetMethod(controller, request, "Request")
			}

		}

		if f != nil {
			handleRequest(f, request, hasAuth, w, r)
		} else {
			// no handler --> search in dist
			handleDist(dist, uri, w, r)
		}
	}
}

func safeExit(sessionDumpPath string) {
	utility.Logf(utility.INFO, "SafeExit")

	chronoSerialize(sessionDumpPath)

	os.Exit(0)
}

func Run(rootController interface{}, dist string, bind string, cert string, key string, sessionDumpPath string) {
	http.HandleFunc("/", getHandler(rootController, dist))

	if err := RestoreSessions(sessionDumpPath); err != nil {
		utility.Logf(utility.ERROR, "could not restore sessions: %s", err.Error())
	}

	go sessionReaper()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM) // -syscall.SIGHUP

	go func() {
		sig := <-sigs
		fmt.Printf("Ricevuto segnale: %v\n", sig)
		safeExit(sessionDumpPath)
	}()

	if sessionDumpPath != "" {
		go func() {
			for {
				time.Sleep(1 * time.Second)
				chronoSerialize(sessionDumpPath)
			}
		}()
	}

	err := http.ListenAndServeTLS(bind, cert, key, nil)

	if err != nil {
		utility.Mypanic(err)
	}
}
