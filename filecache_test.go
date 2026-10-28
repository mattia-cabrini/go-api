// Copyright (C) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package goapi

import (
	"bytes"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// resetCache replaces the global cache with an empty one of the given limit
// and ttl, restoring the default after the test.
func resetCache(t *testing.T, limit int64, ttl time.Duration) *fileCache {
	t.Helper()
	old := cache
	cache = newFileCache(limit, ttl)
	t.Cleanup(func() { cache = old })
	return cache
}

// writeFile writes content under dir and returns its path.
func writeFile(t *testing.T, dir, name string, content []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// getDist serves target through relay and handleDist with the given headers.
func getDist(t *testing.T, dist, method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	relay(func(w http.ResponseWriter, r *http.Request) { handleDist(dist, InitURI(target), w, r) })(rec, req)
	return rec
}

// cachedPaths lists the files in the cache, most recently requested first.
func cachedPaths(c *fileCache) (ps []string) {
	for e := c.lru.Front(); e != nil; e = e.Next() {
		ps = append(ps, filepath.Base(e.Value.(*cachedFile).path))
	}
	return
}

func TestFileCacheHitServesSameFile(t *testing.T) {
	c := resetCache(t, 16<<20, time.Hour)
	dist := t.TempDir()
	p := writeFile(t, dist, "app.js", bigText)
	modtime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	os.Chtimes(p, modtime, modtime)

	for i, want := range []string{"miss", "hit"} {
		rec := getDist(t, dist, "GET", "/app.js", nil)
		if rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), bigText) {
			t.Fatalf("request %d (%s): %d, body differs", i, want, rec.Code)
		}
		if rec.Header().Get("Last-Modified") != modtime.Format(http.TimeFormat) {
			t.Errorf("request %d: Last-Modified = %q", i, rec.Header().Get("Last-Modified"))
		}
		if rec.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("request %d: Cache-Control = %q", i, rec.Header().Get("Cache-Control"))
		}
	}

	if len(c.files) != 1 || c.used != int64(len(bigText)) {
		t.Errorf("cache holds %d files, %d bytes; want 1, %d", len(c.files), c.used, len(bigText))
	}

	// Revalidation is answered from the cache too.
	rec := getDist(t, dist, "GET", "/app.js", map[string]string{"If-Modified-Since": modtime.Format(http.TimeFormat)})
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Errorf("revalidation: %d, %d bytes", rec.Code, rec.Body.Len())
	}

	// Overwriting the file with the same size is not seen before the TTL.
	same := bytes.ToUpper(bigText)
	os.WriteFile(p, same, 0o644)
	if rec = getDist(t, dist, "GET", "/app.js", nil); !bytes.Equal(rec.Body.Bytes(), bigText) {
		t.Errorf("file reloaded within the TTL")
	}
}

func TestFileCacheAnyGETButNoHEAD(t *testing.T) {
	c := resetCache(t, 16<<20, time.Hour)
	dist := t.TempDir()
	writeFile(t, dist, "app.js", bigText)

	if rec := getDist(t, dist, "HEAD", "/app.js", nil); rec.Code != 200 || rec.Body.Len() != 0 || rec.Header().Get("Content-Length") != strconv.Itoa(len(bigText)) {
		t.Errorf("HEAD: %d, %d bytes, Content-Length %q", rec.Code, rec.Body.Len(), rec.Header().Get("Content-Length"))
	}
	if len(c.files) != 0 || FileCacheUsed() != 0 {
		t.Errorf("HEAD populated the cache: %v, %d bytes", cachedPaths(c), FileCacheUsed())
	}

	if rec := getDist(t, dist, "GET", "/app.js?v=2", nil); rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), bigText) {
		t.Errorf("GET with query: %d", rec.Code)
	}
	if len(c.files) != 1 || FileCacheUsed() != len(bigText) {
		t.Errorf("GET with query did not cache the file: %v, %d bytes", cachedPaths(c), FileCacheUsed())
	}

	// The query string does not change the file: same entry, no second load.
	getDist(t, dist, "GET", "/app.js?v=3", nil)
	getDist(t, dist, "GET", "/app.js", nil)
	if len(c.files) != 1 || FileCacheUsed() != len(bigText) {
		t.Errorf("query strings multiplied the entry: %v, %d bytes", cachedPaths(c), FileCacheUsed())
	}
}

func TestFileCacheTooLargeNotKept(t *testing.T) {
	c := resetCache(t, int64(len(bigText))-1, time.Hour)
	dist := t.TempDir()
	writeFile(t, dist, "big.js", bigText)

	for i := 0; i < 2; i++ {
		if rec := getDist(t, dist, "GET", "/big.js", nil); rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), bigText) {
			t.Fatalf("too-large file not served: %d", rec.Code)
		}
	}
	if len(c.files) != 0 || c.used != 0 {
		t.Errorf("too-large file kept: %v, %d bytes", cachedPaths(c), c.used)
	}
}

func TestFileCacheDropsLeastRecentlyRequested(t *testing.T) {
	// Room for two files of bigText, not three.
	c := resetCache(t, int64(2*len(bigText)+10), time.Hour)
	dist := t.TempDir()
	for _, n := range []string{"a.js", "b.js", "c.js", "d.js"} {
		writeFile(t, dist, n, bigText)
	}

	getDist(t, dist, "GET", "/a.js", nil)
	getDist(t, dist, "GET", "/b.js", nil)
	getDist(t, dist, "GET", "/c.js", nil) // a is the least recently requested: dropped

	if got := cachedPaths(c); len(got) != 2 || got[0] != "c.js" || got[1] != "b.js" {
		t.Fatalf("after a, b, c: cache is %v, want [c.js b.js]", got)
	}

	getDist(t, dist, "GET", "/b.js", nil) // b requested again: c becomes the oldest
	getDist(t, dist, "GET", "/d.js", nil) // so c, not b, is dropped

	if got := cachedPaths(c); len(got) != 2 || got[0] != "d.js" || got[1] != "b.js" {
		t.Errorf("after b, d: cache is %v, want [d.js b.js]", got)
	}
	if c.used != int64(2*len(bigText)) {
		t.Errorf("used = %d, want %d", c.used, 2*len(bigText))
	}
}

func TestFileCacheCompressedVariant(t *testing.T) {
	c := resetCache(t, 16<<20, time.Hour)
	dist := t.TempDir()
	p := writeFile(t, dist, "app.js", bigText)

	for i := 0; i < 2; i++ {
		rec := getDist(t, dist, "GET", "/app.js", map[string]string{"Accept-Encoding": "gzip, deflate"})
		if rec.Header().Get("Content-Encoding") != "gzip" {
			t.Fatalf("request %d: Content-Encoding = %q", i, rec.Header().Get("Content-Encoding"))
		}
		if rec.Header().Get("Content-Length") != strconv.Itoa(rec.Body.Len()) || rec.Header().Get("Accept-Ranges") != "" {
			t.Errorf("request %d: Content-Length %q for %d bytes, Accept-Ranges %q", i, rec.Header().Get("Content-Length"), rec.Body.Len(), rec.Header().Get("Accept-Ranges"))
		}
		if rec.Header().Get("Vary") != "Accept-Encoding" {
			t.Errorf("request %d: Vary = %q", i, rec.Header().Get("Vary"))
		}
		if !bytes.Equal(gunzip(t, rec.Body.Bytes()), bigText) {
			t.Errorf("request %d: body does not round-trip", i)
		}
	}

	f := c.files[p]
	gz, ok := f.variants["gzip"]
	if !ok || gz == nil || len(gz) >= len(bigText) {
		t.Fatalf("gzip variant not kept: known=%v len=%d", ok, len(gz))
	}
	if c.used != int64(len(bigText)+len(gz)) {
		t.Errorf("used = %d, want raw + variant = %d", c.used, len(bigText)+len(gz))
	}

	// A client that takes deflate only gets its own variant.
	rec := getDist(t, dist, "GET", "/app.js", map[string]string{"Accept-Encoding": "deflate"})
	if rec.Header().Get("Content-Encoding") != "deflate" || f.variants["deflate"] == nil {
		t.Errorf("deflate: Content-Encoding = %q, variant kept = %v", rec.Header().Get("Content-Encoding"), f.variants["deflate"] != nil)
	}

	// A plain client still gets the raw bytes.
	if rec = getDist(t, dist, "GET", "/app.js", nil); rec.Header().Get("Content-Encoding") != "" || !bytes.Equal(rec.Body.Bytes(), bigText) {
		t.Errorf("identity: Content-Encoding = %q", rec.Header().Get("Content-Encoding"))
	}
}

func TestFileCacheVariantNotKeptWhenNotSmaller(t *testing.T) {
	c := resetCache(t, 16<<20, time.Hour)
	dist := t.TempDir()
	noise := make([]byte, 4096)
	rand.Read(noise)
	p := writeFile(t, dist, "noise.bin", noise)

	rec := getDist(t, dist, "GET", "/noise.bin", map[string]string{"Accept-Encoding": "gzip"})
	if rec.Header().Get("Content-Encoding") != "" || !bytes.Equal(rec.Body.Bytes(), noise) {
		t.Errorf("incompressible file sent encoded: %q", rec.Header().Get("Content-Encoding"))
	}
	if v, known := c.files[p].variants["gzip"]; !known || v != nil {
		t.Errorf("gzip variant: known=%v, kept=%v; want remembered as not smaller", known, v != nil)
	}
	if c.used != int64(len(noise)) {
		t.Errorf("used = %d, want %d", c.used, len(noise))
	}
}

func TestFileCacheRangeAndHTMLStayRaw(t *testing.T) {
	c := resetCache(t, 16<<20, time.Hour)
	dist := t.TempDir()
	writeFile(t, dist, "app.js", bigText)
	shell := []byte(`<!doctype html><html><head>` + shellBase + `</head><body>` + string(bigText) + `</body></html>`)
	writeFile(t, dist, "index.html", shell)

	rec := getDist(t, dist, "GET", "/app.js", map[string]string{"Accept-Encoding": "gzip", "Range": "bytes=0-9"})
	if rec.Code != http.StatusPartialContent || rec.Header().Get("Content-Encoding") != "" || !bytes.Equal(rec.Body.Bytes(), bigText[:10]) {
		t.Errorf("range: %d %q %q", rec.Code, rec.Header().Get("Content-Encoding"), rec.Body.String())
	}

	// The shell comes from the cache raw, and the relay still rebases and
	// compresses it.
	rec = getDist(t, dist, "GET", "/settings/currencies", map[string]string{"Accept-Encoding": "gzip"})
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("shell: Content-Encoding = %q", rec.Header().Get("Content-Encoding"))
	}
	if body := gunzip(t, rec.Body.Bytes()); !bytes.Contains(body, []byte(`<base href="../">`)) {
		t.Errorf("shell not rebased from the cache")
	}
	if f := c.files[filepath.Join(dist, "index.html")]; f == nil || len(f.variants) != 0 {
		t.Errorf("shell not cached raw-only: %v", f)
	}
}

func TestFileCacheReloadsOnSizeChangeAfterTTL(t *testing.T) {
	c := resetCache(t, 16<<20, 0) // every hit checks the disk
	dist := t.TempDir()
	p := writeFile(t, dist, "app.js", bigText)

	getDist(t, dist, "GET", "/app.js", map[string]string{"Accept-Encoding": "gzip"})
	if c.files[p].variants["gzip"] == nil {
		t.Fatal("variant not kept")
	}

	changed := append([]byte("// v2\n"), bigText...)
	os.WriteFile(p, changed, 0o644)

	rec := getDist(t, dist, "GET", "/app.js", nil)
	if !bytes.Equal(rec.Body.Bytes(), changed) {
		t.Errorf("changed file not reloaded")
	}
	if f := c.files[p]; f == nil || len(f.variants) != 0 || c.used != int64(len(changed)) {
		t.Errorf("stale variants survived the reload: used=%d", c.used)
	}
}

func TestSetFileCacheLimitEvicts(t *testing.T) {
	c := resetCache(t, 16<<20, time.Hour)
	dist := t.TempDir()
	for _, n := range []string{"a.js", "b.js", "c.js"} {
		writeFile(t, dist, n, bigText)
		getDist(t, dist, "GET", "/"+n, nil)
	}

	if FileCacheUsed() != 3*len(bigText) {
		t.Errorf("used %d, want %d", FileCacheUsed(), 3*len(bigText))
	}

	SetFileCacheLimit(len(bigText) + 1)
	if got := cachedPaths(c); len(got) != 1 || got[0] != "c.js" {
		t.Errorf("after shrinking: %v, want [c.js]", got)
	}
	if FileCacheUsed() != len(bigText) {
		t.Errorf("used %d after shrinking, want %d", FileCacheUsed(), len(bigText))
	}

	SetFileCacheLimit(16 << 20)
}
