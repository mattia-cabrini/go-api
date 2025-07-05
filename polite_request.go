// Copyright (C) 2025 Mattia Cabrini
// SPDX-License-Identifier: MIT

package goapi

import (
	"bytes"
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
	m := make(map[string]string)
	for k, v := range pr.URL.Query() {
		if len(v) > 0 {
			m[k] = v[0]
		}
	}
	return m
}

// FormParams parses and returns HTML form POST parameters as a map[string]string.
// Assumes fields were submitted via a standard HTML form.
func (pr *PoliteRequest) FormParams() (map[string]string, error) {
	if err := pr.ParseForm(); err != nil {
		return nil, err
	}
	m := make(map[string]string)
	for k, v := range pr.PostForm {
		if len(v) > 0 {
			m[k] = v[0]
		}
	}
	return m, nil
}

// JSONParams parses a JSON POST body (e.g., from an Axios request) and
// returns its contents as a map[string]interface{}.
func (pr *PoliteRequest) JSONParams() (map[string]interface{}, error) {
	var m map[string]interface{}
	defer pr.Body.Close()
	decoder := json.NewDecoder(pr.Body)
	if err := decoder.Decode(&m); err != nil && err != io.EOF {
		return nil, err
	}
	return m, nil
}

// MultipartParams parses a multipart/form-data request and returns:
// - fields: map[string]string of form field values
// - files: map[string][]*multipart.FileHeader of uploaded files
// maxMemory indicates the maximum amount of memory to use for parsing.
func (pr *PoliteRequest) MultipartParams(maxMemory int64) (_ map[string]string, _ map[string][]*multipart.FileHeader, terror error) {
	if err := pr.ParseMultipartForm(maxMemory); err != nil {
		return nil, nil, err
	}
	fields := make(map[string]string)
	for k, v := range pr.MultipartForm.Value {
		if len(v) > 0 {
			fields[k] = v[0]
		}
	}
	files := make(map[string][]*multipart.FileHeader)
	for k, fhs := range pr.MultipartForm.File {
		files[k] = fhs
	}
	return fields, files, nil
}

func (pr PoliteRequest) RetrieveMultipartFileBytes(key string) (buf []byte, h *multipart.FileHeader, err error) {
	const maxUploadSize = 10 << 20 // 10 MB
	var buffer bytes.Buffer
	var fp multipart.File

	err = pr.ParseMultipartForm(maxUploadSize)
	if err == nil {

		fp, h, err = pr.FormFile(key)
		if err == nil {

			defer utility.Deferrable(fp.Close, nil, nil)
			_, err = io.Copy(&buffer, fp)
		}
	}

	buf = buffer.Bytes()
	err = utility.AppendError(err)
	return
}
