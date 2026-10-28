// Copyright (C) 2025-2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package goapi

import (
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/mattia-cabrini/go-utility"
)

// PoliteRequest embeds http.Request and provides helper methods for common tasks.
type PoliteRequest struct {
	*http.Request
}

// initPoliteRequest initializes a PoliteRequest from an *http.Request.
func initPoliteRequest(r *http.Request) PoliteRequest {
	return PoliteRequest{Request: r}
}

// firstValues keeps the first value of each key of a multi-valued form.
func firstValues(form map[string][]string) map[string]string {
	m := make(map[string]string, len(form))
	for k, v := range form {
		if len(v) > 0 {
			m[k] = v[0]
		}
	}
	return m
}

// GetCookie retrieves the value of the cookie with the specified name.
// Returns an error if the cookie does not exist or cannot be accessed.
func (pr *PoliteRequest) GetCookie(name string) (string, error) {
	c, err := pr.Cookie(name)
	if err != nil {
		return "", err
	}
	return c.Value, nil
}

// QueryParams returns the URL query parameters as a map[string]string.
// Only the first value for each key is retained.
func (pr *PoliteRequest) QueryParams() map[string]string {
	return firstValues(pr.URL.Query())
}

// FormParams parses and returns HTML form POST parameters as a map[string]string.
// Assumes fields were submitted via a standard HTML form.
func (pr *PoliteRequest) FormParams() (map[string]string, error) {
	if err := pr.ParseForm(); err != nil {
		return nil, err
	}
	return firstValues(pr.PostForm), nil
}

// JSONParams parses a JSON POST body (e.g., from an Axios request) and
// returns its contents as a map[string]interface{}.
func (pr *PoliteRequest) JSONParams() (map[string]interface{}, error) {
	var m map[string]interface{}
	defer pr.Body.Close()
	if err := json.NewDecoder(pr.Body).Decode(&m); err != nil && err != io.EOF {
		return nil, err
	}
	return m, nil
}

// MultipartParams parses a multipart/form-data request and returns:
// - fields: map[string]string of form field values
// - files: map[string][]*multipart.FileHeader of uploaded files
// maxMemory indicates the maximum amount of memory to use for parsing.
func (pr *PoliteRequest) MultipartParams(maxMemory int64) (map[string]string, map[string][]*multipart.FileHeader, error) {
	if err := pr.ParseMultipartForm(maxMemory); err != nil {
		return nil, nil, err
	}
	return firstValues(pr.MultipartForm.Value), pr.MultipartForm.File, nil
}

// RetrieveMultipartFileBytes returns the content and header of the uploaded
// file under key, parsing the form with at most 10 MiB in memory.
func (pr PoliteRequest) RetrieveMultipartFileBytes(key string) (buf []byte, h *multipart.FileHeader, err error) {
	const maxUploadSize = 10 << 20

	if err = pr.ParseMultipartForm(maxUploadSize); err != nil {
		return nil, nil, utility.AppendError(err)
	}

	fp, h, err := pr.FormFile(key)
	if err != nil {
		return nil, nil, utility.AppendError(err)
	}
	defer fp.Close()

	buf, err = io.ReadAll(fp)
	return buf, h, utility.AppendError(err)
}

// Transform converts the parameters of a request, as the strings they arrive
// in, to the values an API function expects, and may refuse them with an
// error. It is the project's to define: which keys hold integers, amounts,
// dates, and what a malformed one means.
type Transform func(params map[string]string) (map[string]interface{}, error)

// transform is the Transform SetTransform installed, nil for none. It is set
// once at startup, before Run.
var transform Transform

// SetTransform installs the Transform that Convert, and so Params, apply.
// With none installed, parameters pass through unchanged, as strings.
func SetTransform(t Transform) {
	transform = t
}

// Convert applies the installed Transform to params; with none, it returns
// them as they are, as strings.
func Convert(params map[string]string) (map[string]interface{}, error) {
	if transform != nil {
		return transform(params)
	}

	m := make(map[string]interface{}, len(params))
	for k, v := range params {
		m[k] = v
	}

	return m, nil
}

// Params returns the POST form parameters converted by the installed
// Transform (see SetTransform), or as strings when there is none.
func (pr PoliteRequest) Params() (map[string]interface{}, error) {
	fields, err := pr.FormParams()
	if err != nil {
		return nil, err
	}

	return Convert(fields)
}
