// Copyright (C) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package goapi

import (
	"bytes"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// cacheApi is the API of the cache tests' controller tree, mounted at /Api.
type cacheApi struct{}

// cacheController is the root of the cache tests' controller tree.
type cacheController struct {
	Api cacheApi `controller:"true"`
}

func (cacheApi) LoginRequest(s *Session, p PoliteRequest) JsonResponse {
	s.SetUser("alice")
	return InitJsonResponse()
}

// PingRequest answers a plain API GET: no file, no policy of its own.
func (cacheApi) PingRequest(s *Session, p PoliteRequest) JsonResponse {
	jr := InitJsonResponse()
	jr.Set("data", string(bigText))
	return jr
}

// FreshRequest answers with a Cache-Control of its own, which must survive.
func (cacheApi) FreshRequest(s *Session, p PoliteRequest) JsonResponse {
	jr := InitJsonResponse()
	jr.SetHeader("Cache-Control", "public, max-age=60")
	return jr
}

// cacheDist builds a dist with a shell and a compressible asset, both with a
// fixed, whole-second modification time so Last-Modified round-trips exactly.
func cacheDist(t *testing.T) (dist string, modtime time.Time) {
	t.Helper()
	dist = t.TempDir()
	modtime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	shell := []byte(`<!doctype html><html><head>` + shellBase + `</head><body>` + string(bigText) + `</body></html>`)
	os.WriteFile(filepath.Join(dist, "index.html"), shell, 0o644)
	os.Mkdir(filepath.Join(dist, "assets"), 0o755)
	os.WriteFile(filepath.Join(dist, "assets", "app.js"), bigText, 0o644)

	for _, f := range []string{"index.html", "assets/app.js"} {
		if err := os.Chtimes(filepath.Join(dist, f), modtime, modtime); err != nil {
			t.Fatal(err)
		}
	}

	return
}

// cacheServer serves the whole stack, relay over getHandler, over TLS so the
// Secure session cookie is sent back, with a client that keeps cookies and
// does not decode or request compression on its own.
func cacheServer(t *testing.T, dist string) (*httptest.Server, *http.Client) {
	t.Helper()
	resetSessions(t)
	resetCache(t, 16<<20, 0) // files change on disk during these tests: check them on every hit

	srv := httptest.NewTLSServer(relay(getHandler(cacheController{}, dist)))
	t.Cleanup(srv.Close)

	client := srv.Client()
	client.Jar, _ = cookiejar.New(nil)
	client.Transport.(*http.Transport).DisableCompression = true
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	// Log in once, so API calls have a session and are not redirected.
	res, err := client.Get(srv.URL + "/Api/Login")
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("login: %v %v", err, res)
	}
	res.Body.Close()

	return srv, client
}

// fetch performs method on path with the given headers and returns the
// response with its body read.
func fetch(t *testing.T, client *http.Client, method, url string, hdr map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, _ := http.NewRequest(method, url, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res, body
}

func TestCachePolicyByKindOfAnswer(t *testing.T) {
	dist, modtime := cacheDist(t)
	srv, client := cacheServer(t, dist)
	lastModified := modtime.Format(http.TimeFormat)

	cases := []struct {
		name, method, path string
		status             int
		cacheControl       string
		lastModified       bool
	}{
		{"asset is no-cache", "GET", "/assets/app.js", 200, "no-cache", true},
		{"root shell is no-cache", "GET", "/", 200, "no-cache", true},
		{"client route shell is no-cache", "GET", "/settings/currencies", 200, "no-cache", true},
		{"HEAD asset is no-cache", "HEAD", "/assets/app.js", 200, "no-cache", true},
		{"API GET is no-store", "GET", "/Api/Ping", 200, "no-store", false},
		{"handler's own policy kept", "GET", "/Api/Fresh", 200, "public, max-age=60", false},
		{"missing asset is no-store", "GET", "/assets/missing.js", 404, "no-store", false},
		{"POST gets no policy", "POST", "/Api/Ping", 200, "", false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, body := fetch(t, client, c.method, srv.URL+c.path, nil)

			if res.StatusCode != c.status {
				t.Fatalf("status %d, want %d", res.StatusCode, c.status)
			}
			if got := res.Header.Get("Cache-Control"); got != c.cacheControl {
				t.Errorf("Cache-Control = %q, want %q", got, c.cacheControl)
			}
			if got := res.Header.Get("Last-Modified"); c.lastModified && got != lastModified {
				t.Errorf("Last-Modified = %q, want %q", got, lastModified)
			} else if !c.lastModified && got != "" {
				t.Errorf("unexpected Last-Modified %q", got)
			}
			if c.method == "HEAD" && (len(body) != 0 || res.Header.Get("Content-Length") != strconv.Itoa(len(bigText))) {
				t.Errorf("HEAD: body %d bytes, Content-Length %q", len(body), res.Header.Get("Content-Length"))
			}
		})
	}
}

func TestCacheRedirectToLoginIsNoStore(t *testing.T) {
	dist, _ := cacheDist(t)
	resetSessions(t)
	srv := httptest.NewTLSServer(relay(getHandler(cacheController{}, dist)))
	defer srv.Close()

	client := srv.Client() // no jar: every API call is a first visit
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	res, _ := fetch(t, client, "GET", srv.URL+"/Api/Ping", nil)
	if res.StatusCode != http.StatusTemporaryRedirect || res.Header.Get("Location") != "../Login" {
		t.Fatalf("expected redirect to ../Login, got %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("redirect Cache-Control = %q, want no-store so a cached redirect cannot outlive the login", res.Header.Get("Cache-Control"))
	}
}

// TestCacheRevalidation plays the browser's side of no-cache: keep the copy,
// ask before each use, get a cheap 304 while it is current and a fresh 200
// once the file changes. Plain and gzip.
func TestCacheRevalidation(t *testing.T) {
	for _, coding := range []string{"", "gzip"} {
		t.Run("coding="+coding, func(t *testing.T) {
			dist, modtime := cacheDist(t)
			srv, client := cacheServer(t, dist)
			url := srv.URL + "/assets/app.js"
			accept := map[string]string{"Accept-Encoding": coding}

			// 1. First visit: a full answer carrying the validator.
			res, body := fetch(t, client, "GET", url, accept)
			if res.StatusCode != 200 || res.Header.Get("Cache-Control") != "no-cache" {
				t.Fatalf("first GET: %d %q", res.StatusCode, res.Header.Get("Cache-Control"))
			}
			validator := res.Header.Get("Last-Modified")
			if validator != modtime.Format(http.TimeFormat) {
				t.Fatalf("Last-Modified = %q", validator)
			}
			if res.Header.Get("Content-Encoding") != coding {
				t.Fatalf("Content-Encoding = %q, want %q", res.Header.Get("Content-Encoding"), coding)
			}
			if coding == "gzip" {
				body = gunzip(t, body)
			}
			if !bytes.Equal(body, bigText) {
				t.Fatalf("first GET body differs")
			}

			// 2. Revalidation of a current copy: 304, nothing but headers, and
			// the same policy and Vary the 200 had.
			res, body = fetch(t, client, "GET", url, map[string]string{"Accept-Encoding": coding, "If-Modified-Since": validator})
			if res.StatusCode != http.StatusNotModified {
				t.Fatalf("revalidation: status %d, want 304", res.StatusCode)
			}
			if len(body) != 0 {
				t.Errorf("304 carried %d body bytes", len(body))
			}
			if res.Header.Get("Cache-Control") != "no-cache" {
				t.Errorf("304 Cache-Control = %q, want no-cache", res.Header.Get("Cache-Control"))
			}
			if res.Header.Get("Vary") != "Accept-Encoding" {
				t.Errorf("304 Vary = %q, want Accept-Encoding", res.Header.Get("Vary"))
			}
			if res.Header.Get("Content-Encoding") != "" {
				t.Errorf("304 Content-Encoding = %q, want none", res.Header.Get("Content-Encoding"))
			}

			// 3. A copy older than the file is refreshed in full.
			stale := modtime.Add(-time.Hour).Format(http.TimeFormat)
			res, body = fetch(t, client, "GET", url, map[string]string{"Accept-Encoding": coding, "If-Modified-Since": stale})
			if res.StatusCode != 200 || len(body) == 0 {
				t.Errorf("stale copy: status %d, %d body bytes, want a full 200", res.StatusCode, len(body))
			}

			// 4. The file changes: the old validator no longer matches and the
			// new content comes with a new one.
			changed := append([]byte("// v2\n"), bigText...)
			later := modtime.Add(time.Hour)
			os.WriteFile(filepath.Join(dist, "assets", "app.js"), changed, 0o644)
			os.Chtimes(filepath.Join(dist, "assets", "app.js"), later, later)

			res, body = fetch(t, client, "GET", url, map[string]string{"Accept-Encoding": coding, "If-Modified-Since": validator})
			if res.StatusCode != 200 {
				t.Fatalf("changed file: status %d, want 200", res.StatusCode)
			}
			if coding == "gzip" {
				body = gunzip(t, body)
			}
			if !bytes.Equal(body, changed) {
				t.Errorf("changed file: body is not the new content")
			}
			if res.Header.Get("Last-Modified") != later.Format(http.TimeFormat) {
				t.Errorf("changed file: Last-Modified = %q, want %q", res.Header.Get("Last-Modified"), later.Format(http.TimeFormat))
			}

			// 5. And the new copy revalidates in turn.
			res, _ = fetch(t, client, "GET", url, map[string]string{"Accept-Encoding": coding, "If-Modified-Since": res.Header.Get("Last-Modified")})
			if res.StatusCode != http.StatusNotModified {
				t.Errorf("new copy revalidation: status %d, want 304", res.StatusCode)
			}
		})
	}
}

// TestCacheNoStoreNeverRevalidates: an API answer has no validator, so a
// conditional request cannot be answered with 304 and always gets the full,
// current body.
func TestCacheNoStoreNeverRevalidates(t *testing.T) {
	dist, _ := cacheDist(t)
	srv, client := cacheServer(t, dist)

	res, body := fetch(t, client, "GET", srv.URL+"/Api/Ping", map[string]string{"If-Modified-Since": time.Now().Format(http.TimeFormat)})
	if res.StatusCode != 200 || !bytes.Contains(body, []byte(`"session":true`)) {
		t.Errorf("conditional API GET: %d, %d bytes; want a full 200", res.StatusCode, len(body))
	}
	if res.Header.Get("Last-Modified") != "" || res.Header.Get("ETag") != "" {
		t.Errorf("API answer carries a validator: %v", res.Header)
	}
}

// TestCacheShellRebasedPerRoute: the shell is one file but is sent rebased per
// client route, so the Vary and no-cache policy are what keep a cache from
// handing the copy of one route to another.
func TestCacheShellRebasedPerRoute(t *testing.T) {
	dist, modtime := cacheDist(t)
	srv, client := cacheServer(t, dist)

	for route, base := range map[string]string{"/": "./", "/settings": "./", "/settings/currencies": "../", "/a/b/c": "../../"} {
		res, body := fetch(t, client, "GET", srv.URL+route, nil)
		if res.StatusCode != 200 || !bytes.Contains(body, []byte(`<base href="`+base+`">`)) {
			t.Errorf("%s: status %d, base not %q", route, res.StatusCode, base)
		}
		if res.Header.Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: Cache-Control = %q", route, res.Header.Get("Cache-Control"))
		}
		if cl := res.Header.Get("Content-Length"); cl != strconv.Itoa(len(body)) {
			t.Errorf("%s: Content-Length %q for %d bytes", route, cl, len(body))
		}

		res, _ = fetch(t, client, "GET", srv.URL+route, map[string]string{"If-Modified-Since": modtime.Format(http.TimeFormat)})
		if res.StatusCode != http.StatusNotModified {
			t.Errorf("%s: revalidation status %d, want 304", route, res.StatusCode)
		}
	}
}
