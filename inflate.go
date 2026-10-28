// Copyright (C) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package goapi

import (
	"io"
	"net/http"
	"strings"
)

// inflateLimit is the most bytes a request body sent compressed may inflate
// to. See SetInflateLimit.
var inflateLimit = limit(32 << 20)

// SetInflateLimit sets the most bytes a request body sent compressed (gzip or
// deflate Content-Encoding) may inflate to; a body inflating past it fails to
// read and the connection is closed once answered. The default is 32 MiB.
func SetInflateLimit(bytes int) {
	inflateLimit.Store(int64(bytes))
}

// inflated returns the request as the handler is to read it: one whose body
// was sent in one of codings, with the body read inflated and neither the
// Content-Encoding nor the length of the bytes sent; any other, itself. w is
// the raw ResponseWriter, told of a body too large.
func inflated(w http.ResponseWriter, r *http.Request) *http.Request {
	open, known := decoders[strings.ToLower(r.Header.Get("Content-Encoding"))]

	if !known {
		return r
	}

	in := r.Clone(r.Context())
	in.Header.Del("Content-Encoding")
	in.Header.Del("Content-Length")
	in.ContentLength = -1
	in.Body = &inflater{ReadCloser: r.Body, w: w, open: open}

	return in
}

// inflater reads a body sent compressed, inflated, up to inflateLimit bytes.
// The stream is opened at the first read: a request answered with its body
// unread inflates nothing, and a body not in its coding fails the read that
// opens it.
type inflater struct {
	io.ReadCloser                                        // the body as sent
	w             http.ResponseWriter                    // the answer, told of a body too large
	open          func(io.Reader) (io.ReadCloser, error) // the decoder of its coding
	content       io.Reader                              // the inflated content, nil before the first read
}

func (i *inflater) Read(p []byte) (int, error) {
	if i.content == nil {
		z, err := i.open(i.ReadCloser)

		if err != nil {
			return 0, err
		}

		i.content = http.MaxBytesReader(i.w, z, inflateLimit.Load())
	}

	return i.content.Read(p)
}
