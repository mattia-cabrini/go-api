// Copyright (C) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package goapi

import (
	"compress/flate"
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// acceptedEncoding picks the content coding to use for r: "gzip" when the
// client accepts it, otherwise "deflate", otherwise "". Range requests are never
// compressed since byte ranges refer to the uncompressed representation.
func acceptedEncoding(r *http.Request) (best string) {
	if r.Header.Get("Range") != "" {
		return
	}

	for _, part := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		name = strings.ToLower(strings.TrimSpace(name))

		if name != "gzip" && name != "deflate" {
			continue
		}

		if q, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			if f, err := strconv.ParseFloat(q, 64); err == nil && f == 0 {
				continue // explicitly refused
			}
		}

		if name == "gzip" {
			return name
		}

		best = name
	}

	return
}

// compressWriter compresses the response body with the negotiated encoding.
// The status line is deferred to the first body write so bodyless responses
// (redirects, 204, 304) go out untouched and a Content-Length set by the
// handler (e.g. http.ServeFile) can be dropped before the headers are sent.
type compressWriter struct {
	http.ResponseWriter
	encoding    string
	status      int
	wroteHeader bool
	cw          io.WriteCloser
}

func (c *compressWriter) WriteHeader(code int) {
	if c.status == 0 {
		c.status = code
	}
}

func (c *compressWriter) Write(p []byte) (int, error) {
	if !c.wroteHeader {
		// Sniff before compressing, as net/http would otherwise sniff the compressed bytes.
		if h := c.Header(); h.Get("Content-Type") == "" {
			h.Set("Content-Type", http.DetectContentType(p))
		}
		c.flushHeader(true)
	}

	if c.cw != nil {
		return c.cw.Write(p)
	}

	return c.ResponseWriter.Write(p)
}

func (c *compressWriter) flushHeader(body bool) {
	c.wroteHeader = true

	if c.status == 0 {
		c.status = http.StatusOK
	}

	h := c.Header()

	if body && h.Get("Content-Encoding") == "" && c.status >= 200 && c.status != http.StatusNoContent && c.status != http.StatusNotModified {
		h.Del("Content-Length")
		h.Set("Content-Encoding", c.encoding)

		if c.encoding == "gzip" {
			c.cw = gzip.NewWriter(c.ResponseWriter)
		} else {
			c.cw, _ = flate.NewWriter(c.ResponseWriter, flate.DefaultCompression)
		}
	}

	c.ResponseWriter.WriteHeader(c.status)
}

// Close sends the deferred headers if nothing was written and finishes the
// compressed stream.
func (c *compressWriter) Close() {
	if !c.wroteHeader {
		c.flushHeader(false)
	}

	if c.cw != nil {
		c.cw.Close()
	}
}

// compress wraps next so its response body is gzip- or deflate-encoded when
// the client advertises support via Accept-Encoding.
func compress(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")

		enc := acceptedEncoding(r)

		if enc == "" {
			next(w, r)
			return
		}

		cw := &compressWriter{ResponseWriter: w, encoding: enc}
		defer cw.Close()

		next(cw, r)
	}
}
