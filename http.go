// Copyright (C) 2025 Mattia Cabrini
// SPDX-License-Identifier: MIT

package goapi

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mattia-cabrini/go-utility"
)

func startSession(w http.ResponseWriter, r *http.Request) (s *Session, b bool, err error) {
	c, err := r.Cookie("sessionid")

	if err == http.ErrNoCookie {
		s, err = newSession("")
		b = true
	} else {
		s, err = newSession(c.Value)
	}

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
		resp := InitJsonResponse()
		resp.Set("data", respi)
	}

	resp.Write(w)
}

func handleDist(dist string, uri URI, w http.ResponseWriter, r *http.Request) {
	uri.ResetStack()

	err := handleFile(dist, &uri, w, r)

	if err != nil {
		utility.Logf(utility.INFO, "not found `%s`"+uri.path)
		http.NotFound(w, r)
	}
}

func handleFile(filePath string, uri *URI, w http.ResponseWriter, r *http.Request) (err error) {
	var s os.FileInfo
	var part = ""

	if uri != nil {
		part = uri.Pop()
	}

	err = os.ErrNotExist

	// depth first
	if part != "" {
		err = handleFile(filePath+"/"+part, uri, w, r)
	}

	if err != nil {
		s, err = os.Stat(filePath)

		if err == nil {
			if s.IsDir() {
				err = handleFile(filePath+"/"+"index.html", nil, w, r)
			} else {
				http.ServeFile(w, r, filePath)
			}
		}
	}

	return err
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
