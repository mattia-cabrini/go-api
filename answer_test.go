// Copyright (C) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package goapi

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/mattia-cabrini/go-utility"
)

var bigText = bytes.Repeat([]byte("the quick brown fox jumps over the lazy dog\n"), 100)

// relayed serves target through relay with the given handler, headers set on
// the request, and an optional body.
func relayed(t *testing.T, method, target string, hdr map[string]string, body io.Reader, h http.HandlerFunc) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, body)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	relay(h)(rec, req)
	return rec
}

func serveText(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.Write(bigText)
}

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	return out
}

func TestAnswerGzipHeldWithContentLength(t *testing.T) {
	rec := relayed(t, "GET", "/Api/Text", map[string]string{"Accept-Encoding": "gzip, deflate, br"}, nil, serveText)

	if ce := rec.Header().Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", ce)
	}
	if cl := rec.Header().Get("Content-Length"); cl != strconv.Itoa(rec.Body.Len()) {
		t.Errorf("Content-Length = %q, body is %d", cl, rec.Body.Len())
	}
	if v := rec.Header().Get("Vary"); v != "Accept-Encoding" {
		t.Errorf("Vary = %q", v)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store for a non-file GET", cc)
	}
	if got := gunzip(t, rec.Body.Bytes()); !bytes.Equal(got, bigText) {
		t.Errorf("body does not round-trip")
	}
}

func TestAnswerDeflateIsZlib(t *testing.T) {
	rec := relayed(t, "GET", "/t", map[string]string{"Accept-Encoding": "gzip;q=0, deflate"}, nil, serveText)

	if ce := rec.Header().Get("Content-Encoding"); ce != "deflate" {
		t.Fatalf("Content-Encoding = %q, want deflate", ce)
	}
	zr, err := zlib.NewReader(rec.Body)
	if err != nil {
		t.Fatalf("deflate body is not zlib: %v", err)
	}
	if got, _ := io.ReadAll(zr); !bytes.Equal(got, bigText) {
		t.Errorf("body does not round-trip")
	}
}

func TestAnswerIdentityWhenNotSmaller(t *testing.T) {
	rec := relayed(t, "GET", "/t", map[string]string{"Accept-Encoding": "gzip"}, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("hi"))
	})

	if ce := rec.Header().Get("Content-Encoding"); ce != "" {
		t.Errorf("tiny body compressed: Content-Encoding = %q", ce)
	}
	if rec.Body.String() != "hi" || rec.Header().Get("Content-Length") != "2" {
		t.Errorf("body %q, Content-Length %q", rec.Body.String(), rec.Header().Get("Content-Length"))
	}
}

func TestAnswerCompressesAnyTypeWhenSmaller(t *testing.T) {
	rec := relayed(t, "GET", "/i.png", map[string]string{"Accept-Encoding": "gzip"}, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(bigText) // compressible despite its type
	})

	if rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Vary") != "Accept-Encoding" {
		t.Errorf("compressible image body not compressed: %v", rec.Header())
	}
	if got := gunzip(t, rec.Body.Bytes()); !bytes.Equal(got, bigText) {
		t.Errorf("body does not round-trip")
	}

	rec = relayed(t, "GET", "/e", map[string]string{"Accept-Encoding": "gzip"}, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Encoding", "br")
		w.Write(bigText)
	})

	if rec.Header().Get("Content-Encoding") != "br" || !bytes.Equal(rec.Body.Bytes(), bigText) {
		t.Errorf("already encoded body re-encoded: %v", rec.Header())
	}
}

func TestAnswerStreamsPastHoldLimit(t *testing.T) {
	SetHoldLimit(100)
	defer SetHoldLimit(16 << 20)

	rec := relayed(t, "GET", "/t", map[string]string{"Accept-Encoding": "gzip"}, nil, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Content-Length", strconv.Itoa(len(bigText)))
		w.Header().Set("Accept-Ranges", "bytes")
		for i := 0; i < len(bigText); i += 50 {
			w.Write(bigText[i:min(i+50, len(bigText))])
		}
	})

	if ce := rec.Header().Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", ce)
	}
	if rec.Header().Get("Content-Length") != "" || rec.Header().Get("Accept-Ranges") != "" {
		t.Errorf("streamed answer kept Content-Length/Accept-Ranges: %v", rec.Header())
	}
	if got := gunzip(t, rec.Body.Bytes()); !bytes.Equal(got, bigText) {
		t.Errorf("streamed body does not round-trip")
	}
}

func TestAnswerRebasesLocation(t *testing.T) {
	redirect := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/Login")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}

	rec := relayed(t, "GET", "/Api/Settings/Get", nil, nil, redirect)
	if loc := rec.Header().Get("Location"); loc != "../../Login" {
		t.Errorf("Location = %q, want ../../Login", loc)
	}
	if rec.Code != http.StatusTemporaryRedirect || rec.Body.Len() != 0 || rec.Header().Get("Content-Encoding") != "" {
		t.Errorf("redirect altered: %d %v %d", rec.Code, rec.Header(), rec.Body.Len())
	}

	rec = relayed(t, "GET", "/Login", nil, nil, redirect)
	if loc := rec.Header().Get("Location"); loc != "./Login" {
		t.Errorf("Location at root = %q, want ./Login", loc)
	}
}

func TestAnswerRebasesShellAndCachesFiles(t *testing.T) {
	dist := t.TempDir()
	shell := []byte(`<!doctype html><html><head>` + shellBase + `</head><body>x</body></html>`)
	os.WriteFile(filepath.Join(dist, "index.html"), shell, 0o644)
	serve := func(w http.ResponseWriter, r *http.Request) { http.ServeFile(w, r, filepath.Join(dist, "index.html")) }

	rec := relayed(t, "GET", "/settings/currencies", nil, nil, serve)
	want := strings.Replace(string(shell), shellBase, `<base href="../">`, 1)
	if rec.Body.String() != want {
		t.Errorf("shell not rebased:\n%s", rec.Body.String())
	}
	if cl := rec.Header().Get("Content-Length"); cl != strconv.Itoa(len(want)) {
		t.Errorf("Content-Length = %q, want %d", cl, len(want))
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache for a file", cc)
	}

	rec = relayed(t, "GET", "/", nil, nil, serve)
	if !bytes.Equal(rec.Body.Bytes(), shell) {
		t.Errorf("shell at the root altered")
	}

	rec = relayed(t, "HEAD", "/", map[string]string{"Accept-Encoding": "gzip"}, nil, serve)
	if rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != strconv.Itoa(len(shell)) || rec.Header().Get("Content-Encoding") != "" {
		t.Errorf("HEAD altered: %v %d", rec.Header(), rec.Body.Len())
	}

	rec = relayed(t, "GET", "/", map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=0-9"}, nil, serve)
	if rec.Code != http.StatusPartialContent || rec.Body.Len() != 10 || rec.Header().Get("Content-Encoding") != "" {
		t.Errorf("range: %d %v %d", rec.Code, rec.Header(), rec.Body.Len())
	}
}

func TestInflatedRequestBody(t *testing.T) {
	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	zw.Write(bigText)
	zw.Close()

	var got []byte
	rec := relayed(t, "POST", "/Api/Post", map[string]string{"Content-Encoding": "gzip"}, &zipped, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Encoding") != "" || r.ContentLength != -1 {
			t.Errorf("inflated request still declares %q / %d", r.Header.Get("Content-Encoding"), r.ContentLength)
		}
		got, _ = io.ReadAll(r.Body)
	})

	if rec.Code != http.StatusOK || !bytes.Equal(got, bigText) {
		t.Errorf("handler read %d bytes, want %d", len(got), len(bigText))
	}
	if rec.Header().Get("Cache-Control") != "" {
		t.Errorf("POST answer got a cache policy: %q", rec.Header().Get("Cache-Control"))
	}
}

func TestInflateLimit(t *testing.T) {
	SetInflateLimit(100)
	defer SetInflateLimit(32 << 20)

	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	zw.Write(bigText)
	zw.Close()

	var err error
	relayed(t, "POST", "/p", map[string]string{"Content-Encoding": "gzip"}, &zipped, func(w http.ResponseWriter, r *http.Request) {
		_, err = io.ReadAll(r.Body)
	})

	if err == nil {
		t.Errorf("body past the inflate limit read without error")
	}
}

func TestCallLog(t *testing.T) {
	stderr, level := os.Stderr, utility.MinimumLevel
	f, _ := os.CreateTemp(t.TempDir(), "log")
	os.Stderr, utility.MinimumLevel = f, utility.VERBOSE
	defer func() { os.Stderr, utility.MinimumLevel = stderr, level }()

	before := callCount.Load()
	relayed(t, "POST", "/Api/FormState?x=1", map[string]string{"Accept-Encoding": "gzip"}, nil, serveText)
	f.Close()

	if callCount.Load() != before+1 {
		t.Errorf("call counter did not advance")
	}
	out, _ := os.ReadFile(f.Name())
	line := regexp.MustCompile(`call \d+: POST /Api/FormState 200; init \d{4}-\d\d-\d\d \d\d:\d\d:\d\d\.\d{3} \w+, closed \d{4}-\d\d-\d\d \d\d:\d\d:\d\d\.\d{3} \w+, net elab [\d.]+(µs|ms|s), gross elab [\d.]+(µs|ms|s); gzip; written [\d.]+ (B|KiB) \(\d+ \+ \d+\), sent [\d.]+ (B|KiB) \(\d+ \+ \d+\)`)
	if !line.Match(out) {
		t.Errorf("call log does not match:\n%s", out)
	}

	utility.MinimumLevel = utility.WARNING
	relayed(t, "GET", "/t", nil, nil, serveText)
	if callCount.Load() != before+1 {
		t.Errorf("call counter advanced below VERBOSE")
	}
}
