// Copyright (C) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package goapi

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mattia-cabrini/go-utility"
)

// limit returns a size limit set to bytes, for a public setter to change while
// serving.
func limit(bytes int64) *atomic.Int64 {
	l := new(atomic.Int64)
	l.Store(bytes)
	return l
}

// holdLimit is how many body bytes an answer is held back before it streams.
// See SetHoldLimit.
var holdLimit = limit(16 << 20)

// SetHoldLimit sets how many body bytes an answer to be compressed is held in
// memory before it is streamed. A body that ends within the limit is
// compressed only where that makes it smaller and goes out with its
// Content-Length; one that grows past the limit is streamed compressed from
// then on, the held part first. The default is 16 MiB.
func SetHoldLimit(bytes int) {
	holdLimit.Store(int64(bytes))
}

// callCount numbers the calls relay logs. It advances only while the log level
// admits VERBOSE lines.
var callCount atomic.Uint64

// shellBase is the base an SPA shell declares: the directory it lies in, the
// root of the frontend. Served for a client route below the root, the shell
// gets it rewritten to point back at that root (see answerWriter).
const shellBase = `<base href="./">`

// logTime is the layout of the instants in the call log, in utility.LogTimeZone.
const logTime = "2006-01-02 15:04:05.000 MST"

// relay wraps next so that it reads its request inflated (inflated), answers
// through an answerWriter, and has each call logged at VERBOSE level once the
// answer is out, as
//
//	call N: METHOD /path STATUS; init T0, closed T1, net elab D, gross elab D'; CODING; written B (headers + body), sent B' (headers + body)
//
// where net elab is the time spent in next and gross elab the time from the
// request's arrival to the answer's completion; written counts the headers and
// body as next produced them, sent what went on the wire. N is process-wide.
func relay(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		arrived := time.Now()
		a := answerOf(w, r)

		next(a, inflated(w, r))

		netElab := time.Since(arrived)
		a.finish()
		closed := time.Now()

		if utility.MinimumLevel >= utility.VERBOSE {
			utility.Logf(utility.VERBOSE, "call %d: %s %s %d; init %s, closed %s, net elab %s, gross elab %s; %s; written %s (%d + %d), sent %s (%d + %d)",
				callCount.Add(1), r.Method, r.URL.Path, a.status,
				arrived.In(utility.LogTimeZone).Format(logTime), closed.In(utility.LogTimeZone).Format(logTime),
				netElab.Round(time.Microsecond), closed.Sub(arrived).Round(time.Microsecond),
				a.sentCoding(),
				sizeOf(int64(a.writtenHeaders)+a.writtenBody), a.writtenHeaders, a.writtenBody,
				sizeOf(int64(a.sentHeaders)+a.wire.n), a.sentHeaders, a.wire.n)
		}
	}
}

// sizeOf formats a byte count for the call log: bytes below 1 KiB, otherwise
// one decimal of the largest fitting binary unit.
func sizeOf(n int64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	f, i := float64(n), 0

	for ; f >= 1024 && i < len(units)-1; i++ {
		f /= 1024
	}

	if i == 0 {
		return fmt.Sprintf("%d B", n)
	}

	return fmt.Sprintf("%.1f %s", f, units[i])
}

// rootOf is the reference from a path to the root of the paths served: "./"
// from a name at the root, one "../" per directory below it. Resolved against
// the address a client reached the path by, it names the root under whatever
// prefix a proxy mounts the server at.
func rootOf(path string) string {
	if n := strings.Count(path, "/") - 1; n > 0 {
		return strings.Repeat("../", n)
	}

	return "./"
}

// countWriter counts the bytes written through it.
type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (n int, err error) {
	n, err = c.w.Write(p)
	c.n += int64(n)
	return
}

// headerSize is the size of a status line and headers as serialized.
func headerSize(code int, h http.Header) int {
	c := countWriter{w: io.Discard}
	h.Write(&c)
	return len("HTTP/1.1 ") + len(strconv.Itoa(code)) + 1 + len(http.StatusText(code)) + 2 + int(c.n) + 2
}

// answerWriter writes a handler's answer, deciding as the headers are sent:
//
//   - a Location that is an absolute path is made relative to the request's
//     path (rootOf), so that it resolves under the prefix the client reached
//     the server by;
//   - the answer to a GET or a HEAD that sets no Cache-Control gets one: a
//     file (Last-Modified set) is no-cache, so the browser keeps it but
//     revalidates before each use; anything else is no-store;
//   - an HTML file sent to a GET below the root is an SPA shell for a client
//     route: it is held back and sent with shellBase rebased to the root;
//   - every whole body not yet encoded varies with Accept-Encoding; to a
//     client that takes one of codings it is held back up to holdLimit and
//     compressed where that makes it smaller (so images and archives usually
//     go as they are), or streamed compressed past the limit.
//
// It counts the headers and body as the handler produced them and as sent,
// for relay's call log.
type answerWriter struct {
	http.ResponseWriter
	wire    *countWriter // the ResponseWriter, counting the body sent
	root    string       // rootOf the request's path
	cached  bool         // whether the caching policy applies: a GET's or a HEAD's
	rebased bool         // whether an HTML file is a shell to rebase: a GET's below the root
	coding  string       // codingOf the request, "" for none

	status  int
	decided bool           // WriteHeader seen
	sent    bool           // headers passed on
	shell   bool           // the body is a shell to rebase
	pack    bool           // the body is to compress
	packed  bool           // the body went compressed
	held    *bytes.Buffer  // the body held back, nil when streaming
	zw      io.WriteCloser // the streaming compressor, nil when none

	writtenHeaders, sentHeaders int
	writtenBody                 int64
}

// answerOf returns the answerWriter of the answer to r, written to w.
func answerOf(w http.ResponseWriter, r *http.Request) *answerWriter {
	root := rootOf(r.URL.Path)

	return &answerWriter{
		ResponseWriter: w,
		wire:           &countWriter{w: w},
		root:           root,
		cached:         r.Method == http.MethodGet || r.Method == http.MethodHead,
		rebased:        r.Method == http.MethodGet && root != "./",
		coding:         codingOf(r),
	}
}

func (a *answerWriter) WriteHeader(code int) {
	if a.decided {
		return
	}

	a.decided, a.status = true, code
	h := a.Header()
	a.writtenHeaders = headerSize(code, h)

	if loc := h.Get("Location"); strings.HasPrefix(loc, "/") && !strings.HasPrefix(loc, "//") {
		h.Set("Location", a.root+loc[1:])
	}

	file := h.Get("Last-Modified") != ""

	if a.cached && h.Get("Cache-Control") == "" {
		if file {
			h.Set("Cache-Control", "no-cache")
		} else {
			h.Set("Cache-Control", "no-store")
		}
	}

	a.shell = a.rebased && file && code == http.StatusOK && strings.HasPrefix(h.Get("Content-Type"), "text/html")

	// Every body is compressed unless that would break it: a partial body
	// (ranges refer to the uncompressed bytes), one already encoded, or an
	// answer that has no body at all.
	whole := code >= 200 && code != http.StatusNoContent && code != http.StatusNotModified && code != http.StatusPartialContent
	compressible := whole && h.Get("Content-Encoding") == ""

	if compressible {
		h.Add("Vary", "Accept-Encoding")
	}

	a.pack = compressible && a.coding != ""

	if a.shell || a.pack {
		a.held = &bytes.Buffer{}
		return
	}

	a.send()
}

// Write sends the headers first, as the implicit 200 of a body written with no
// WriteHeader would, sniffing the Content-Type net/http would otherwise sniff
// from the compressed bytes; the body of an answer held back is held with it.
func (a *answerWriter) Write(p []byte) (int, error) {
	if !a.decided {
		if h := a.Header(); h.Get("Content-Type") == "" {
			h.Set("Content-Type", http.DetectContentType(p))
		}

		a.WriteHeader(http.StatusOK)
	}

	a.writtenBody += int64(len(p))

	if a.held != nil {
		a.held.Write(p)

		if int64(a.held.Len()) > holdLimit.Load() {
			a.stream()
		}

		return len(p), nil
	}

	return a.put(p)
}

// put writes body bytes on, through the compressor where one streams.
func (a *answerWriter) put(p []byte) (int, error) {
	if a.zw != nil {
		return a.zw.Write(p)
	}

	return a.wire.Write(p)
}

// send passes the headers on, once.
func (a *answerWriter) send() {
	if a.sent {
		return
	}

	a.sent = true
	a.sentHeaders = headerSize(a.status, a.Header())
	a.ResponseWriter.WriteHeader(a.status)
}

// body returns the body held back, a shell with its base rebased.
func (a *answerWriter) body() []byte {
	b := a.held.Bytes()
	a.held = nil

	if a.shell {
		b = bytes.Replace(b, []byte(shellBase), []byte(`<base href="`+a.root+`">`), 1)
	}

	return b
}

// stream gives up holding a body that outgrew holdLimit: the headers go out
// with no Content-Length, nor Accept-Ranges where the body is compressed (a
// range would be one of the body as written), and the held part streams on.
func (a *answerWriter) stream() {
	body := a.body()
	a.Header().Del("Content-Length")

	if a.pack {
		a.markPacked()
	}

	a.send()

	if a.pack {
		a.zw = encoders[a.coding](a.wire)
	}

	a.put(body)
}

// markPacked declares the body compressed: it goes out in a.coding, and with no
// Accept-Ranges, as a range would be one of the body as written.
func (a *answerWriter) markPacked() {
	a.Header().Set("Content-Encoding", a.coding)
	a.Header().Del("Accept-Ranges")
	a.packed = true
}

// finish sends what is still held back, compressed where that makes it
// smaller, with the Content-Length of the body as sent; or closes the
// compressor that streamed.
func (a *answerWriter) finish() {
	if !a.decided {
		a.WriteHeader(http.StatusOK)
	}

	if a.held != nil {
		body := a.body()
		h := a.Header()

		if a.pack {
			if packed := encoded(a.coding, body); len(packed) < len(body) {
				body = packed
				a.markPacked()
			}
		}

		h.Set("Content-Length", strconv.Itoa(len(body)))
		a.send()
		a.wire.Write(body)
	}

	if a.zw != nil {
		a.zw.Close()
	}
}

// sentCoding is the coding the body went in, for the call log.
func (a *answerWriter) sentCoding() string {
	if a.packed {
		return a.coding
	}

	return "identity"
}
