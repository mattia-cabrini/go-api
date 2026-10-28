// Copyright (C) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package goapi

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// The content codings served and accepted, all from the standard library:
// codings in the order preferred where a client weighs them alike; encoders
// write a body in each, decoders read one sent in each. HTTP's "deflate" is
// the zlib format (RFC 9110 §8.4.1.1), not a raw DEFLATE stream.
var (
	codings  = []string{"gzip", "deflate"}
	encoders = map[string]func(io.Writer) io.WriteCloser{
		"gzip":    func(w io.Writer) io.WriteCloser { return gzip.NewWriter(w) },
		"deflate": func(w io.Writer) io.WriteCloser { return zlib.NewWriter(w) },
	}
	decoders = map[string]func(io.Reader) (io.ReadCloser, error){
		"gzip":    func(r io.Reader) (io.ReadCloser, error) { return gzip.NewReader(r) },
		"deflate": zlib.NewReader,
	}
)

// encoded returns body written in coding.
func encoded(coding string, body []byte) []byte {
	var b bytes.Buffer
	w := encoders[coding](&b)
	w.Write(body)
	w.Close()
	return b.Bytes()
}

// codingOf returns the coding the answer to r may go in: of codings, the one
// its Accept-Encoding weighs most with a weight above zero, the first on a
// tie; "" where it takes none of them, and for a HEAD, which has no body.
func codingOf(r *http.Request) string {
	if r.Method == http.MethodHead {
		return ""
	}

	weights := map[string]float64{}

	for _, item := range strings.Split(r.Header.Get("Accept-Encoding"), ",") {
		coding, params, _ := strings.Cut(item, ";")
		weights[strings.ToLower(strings.TrimSpace(coding))] = weightOf(params)
	}

	best, top := "", 0.0

	for _, c := range codings {
		if weights[c] > top {
			best, top = c, weights[c]
		}
	}

	return best
}

// weightOf returns the weight an Accept-Encoding item gives its coding: its
// q, 1 where it gives none, 0 where it is not a number.
func weightOf(params string) float64 {
	q, weighed := strings.CutPrefix(strings.TrimSpace(params), "q=")

	if !weighed {
		return 1
	}

	w, err := strconv.ParseFloat(q, 64)

	if err != nil {
		return 0
	}

	return w
}
