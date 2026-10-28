// Copyright (C) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package goapi

import (
	"bytes"
	"container/list"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mattia-cabrini/go-utility"
)

// cachedFile is a file kept in RAM: its bytes as on disk and, per coding
// clients asked for, compressed, nil where compression did not make it smaller.
type cachedFile struct {
	path     string
	size     int64 // size on disk when loaded; a different size means the file changed
	modtime  time.Time
	raw      []byte
	variants map[string][]byte
	checked  time.Time     // when the file was last compared with the disk
	elem     *list.Element // its place in the LRU list
}

// bytes is how much of the cache the file takes: raw bytes and variants.
func (f *cachedFile) bytes() (n int64) {
	n = int64(len(f.raw))
	for _, v := range f.variants {
		n += int64(len(v))
	}
	return
}

// fileCache keeps the files served from dist in RAM, up to limit bytes, the
// least recently requested dropped first to make room. A file larger than the
// limit is never kept. A kept file is compared with the disk, by size, at most
// once per ttl, and reloaded when it changed.
type fileCache struct {
	mu    sync.Mutex
	limit int64
	ttl   time.Duration
	used  int64
	files map[string]*cachedFile
	lru   *list.List // front: most recently requested
}

var cache = newFileCache(16<<20, 30*time.Second)

func newFileCache(limit int64, ttl time.Duration) *fileCache {
	return &fileCache{limit: limit, ttl: ttl, files: map[string]*cachedFile{}, lru: list.New()}
}

// SetFileCacheLimit sets how many bytes of files, raw and compressed, the
// cache may hold; the least recently requested are dropped to fit. A file
// larger than the limit is never cached. The default is 16 MiB.
func SetFileCacheLimit(bytes int) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.limit = int64(bytes)
	cache.evict(0, nil)
}

// FileCacheUsed returns how many bytes of files, raw and compressed, the cache
// holds right now.
func FileCacheUsed() int {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return int(cache.used)
}

// SetFileCacheTTL sets how long a cached file is trusted before its size is
// compared with the disk again. The default is 30 seconds.
func SetFileCacheTTL(d time.Duration) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.ttl = d
}

// log writes a VERBOSE line about the cache: the event (hit, miss, included,
// dropped, stat), the file, a detail, and the fill. The caller holds c.mu.
func (c *fileCache) log(event, file, detail string) {
	if utility.MinimumLevel >= utility.VERBOSE {
		utility.Logf(utility.VERBOSE, "cache %s %s%s; cache %s of %s", event, file, detail, sizeOf(c.used), sizeOf(c.limit))
	}
}

// lookup returns the file at p from the cache, loading it on a miss or when
// its size on disk changed; nil when it cannot be cached.
func (c *fileCache) lookup(p string) *cachedFile {
	c.mu.Lock()
	defer c.mu.Unlock()

	f, ok := c.files[p]

	if ok && time.Since(f.checked) > c.ttl {
		if st, err := os.Stat(p); err == nil && st.Size() == f.size {
			f.checked = time.Now()
			c.log("stat", p, " unchanged")
		} else {
			c.log("stat", p, " changed")
			c.drop(f, "changed on disk")
			ok = false
		}
	}

	if ok {
		c.lru.MoveToFront(f.elem)
		c.log("hit", p, fmt.Sprintf(" (%s)", sizeOf(f.bytes())))
		return f
	}

	c.log("miss", p, "")

	return c.load(p)
}

// load reads p into the cache, dropping the least recently requested files to
// make room; nil for anything but a regular file that fits the limit. The
// caller holds c.mu.
func (c *fileCache) load(p string) *cachedFile {
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() {
		return nil
	}

	if st.Size() > c.limit {
		c.log("dropped", p, fmt.Sprintf(" (%s): does not fit", sizeOf(st.Size())))
		return nil
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		return nil
	}

	c.evict(int64(len(raw)), nil)

	f := &cachedFile{path: p, size: st.Size(), modtime: st.ModTime(), raw: raw, variants: map[string][]byte{}, checked: time.Now()}
	f.elem = c.lru.PushFront(f)
	c.files[p] = f
	c.used += int64(len(raw))
	c.log("included", p, fmt.Sprintf(" (%s)", sizeOf(int64(len(raw)))))

	return f
}

// evict drops the least recently requested files, keep excepted, until need
// more bytes fit; it reports whether they do. The caller holds c.mu.
func (c *fileCache) evict(need int64, keep *cachedFile) bool {
	for c.used+need > c.limit {
		back := c.lru.Back()
		if back == nil || back.Value == keep {
			return false
		}
		c.drop(back.Value.(*cachedFile), "least recently requested")
	}
	return true
}

// drop removes f from the cache. The caller holds c.mu.
func (c *fileCache) drop(f *cachedFile, why string) {
	c.lru.Remove(f.elem)
	delete(c.files, f.path)
	c.used -= f.bytes()
	c.log("dropped", f.path, fmt.Sprintf(" (%s): %s", sizeOf(f.bytes()), why))
}

// variant returns f in coding: compressed on the first request and kept where
// that made it smaller, otherwise the raw bytes and "".
func (c *fileCache) variant(f *cachedFile, coding string) ([]byte, string) {
	c.mu.Lock()
	v, known := f.variants[coding]
	c.mu.Unlock()

	if !known {
		if v = encoded(coding, f.raw); len(v) >= len(f.raw) {
			v = nil
		}

		c.mu.Lock()
		if _, known = f.variants[coding]; !known && c.files[f.path] == f { // not raced, not dropped meanwhile
			if v == nil {
				f.variants[coding] = nil // remember that compression does not pay
			} else if c.evict(int64(len(v)), f) {
				f.variants[coding] = v
				c.used += int64(len(v))
				c.log("included", f.path, fmt.Sprintf(" %s variant (%s)", coding, sizeOf(int64(len(v)))))
			}
		}
		c.mu.Unlock()
	}

	if v == nil {
		return f.raw, ""
	}

	return v, coding
}

// serve answers a request for the file at p. A GET, whatever its query
// string, is served from the cache, in the coding the client takes best where
// a kept variant is smaller and safe to send: not for HTML, which the
// answerWriter may rebase, nor for a range request, whose ranges are of the
// bytes as written. Any other method goes through http.ServeFile.
func (c *fileCache) serve(w http.ResponseWriter, r *http.Request, p string) {
	var f *cachedFile

	if r.Method == http.MethodGet {
		f = c.lookup(p)
	}

	if f == nil {
		http.ServeFile(w, r, p)
		return
	}

	body, coding := f.raw, ""

	if r.Header.Get("Range") == "" && !strings.HasPrefix(mime.TypeByExtension(path.Ext(p)), "text/html") {
		if coding = codingOf(r); coding != "" {
			body, coding = c.variant(f, coding)
		}
	}

	if coding != "" {
		w.Header().Set("Content-Encoding", coding)
		w.Header().Set("Content-Length", strconv.Itoa(len(body))) // ServeContent leaves it out for an encoded body
	}

	http.ServeContent(w, r, p, f.modtime, bytes.NewReader(body))
}
