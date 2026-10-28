// Copyright (C) 2025-2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package goapi

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// Response is the base interface for all HTTP responses.
type Response interface {
	// Write writes headers, status and body to the provided http.ResponseWriter.
	Write(w http.ResponseWriter)
}

// BaseResponse provides common functionality for building HTTP responses.
type BaseResponse struct {
	headers map[string]string
	status  int
}

// newBaseResponse initializes a BaseResponse with default status 200 OK.
func newBaseResponse() *BaseResponse {
	return &BaseResponse{
		headers: make(map[string]string),
		status:  http.StatusOK,
	}
}

// SetHeader sets an HTTP header for the response.
func (b *BaseResponse) SetHeader(key, value string) {
	if b.headers == nil {
		b.headers = make(map[string]string)
	}
	b.headers[key] = value
}

// SetStatus sets the HTTP status code for the response.
func (b *BaseResponse) SetStatus(code int) {
	b.status = code
}

// apply writes headers and status code to the writer.
func (b *BaseResponse) apply(w http.ResponseWriter) {
	for k, v := range b.headers {
		w.Header().Set(k, v)
	}
	w.WriteHeader(b.status)
}

// JsonResponse represents a JSON HTTP response.
type JsonResponse struct {
	*BaseResponse
	data map[string]interface{}
}

// InitJsonResponse creates a JsonResponse with default "session": true, an
// empty "errors" list and JSON content-type.
func InitJsonResponse() (jr JsonResponse) {
	jr.ensure()
	return
}

// ensure initializes BaseResponse, data map and default fields if they are not
// yet initialized, so a zero JsonResponse behaves like InitJsonResponse's.
func (jr *JsonResponse) ensure() {
	if jr.BaseResponse == nil {
		jr.BaseResponse = newBaseResponse()
	}
	if jr.data == nil {
		jr.data = map[string]interface{}{"session": true}
		jr.SetHeader("Content-Type", "application/json")
	}
	if _, ok := jr.data["errors"]; !ok {
		jr.data["errors"] = []string{}
	}
}

// Set adds or updates a field in the JSON body.
func (jr *JsonResponse) Set(key string, value interface{}) {
	jr.ensure()
	jr.data[key] = value
}

// SetSession sets the "session" field to true or false.
func (jr *JsonResponse) SetSession(valid bool) {
	jr.Set("session", valid)
}

// AppendError500 appends err and sets status Internal Server Error; a nil err
// does nothing.
func (jr *JsonResponse) AppendError500(err error) {
	if err != nil {
		jr.AppendError(err)
		jr.SetStatus(http.StatusInternalServerError)
	}
}

// AppendError adds an error to the errors slice in the JSON body.
func (jr *JsonResponse) AppendError(err error) {
	jr.AppendErrorStr(err.Error())
}

// AppendErrorStr adds an error message to the errors slice in the JSON body.
func (jr *JsonResponse) AppendErrorStr(err string) {
	jr.ensure()
	jr.data["errors"] = append(jr.data["errors"].([]string), err)
}

// Write serializes the JSON body and writes it to the ResponseWriter.
// Value receiver ensures JsonResponse can be used as a Response.
func (jr JsonResponse) Write(w http.ResponseWriter) {
	jr.ensure()
	jr.apply(w)
	json.NewEncoder(w).Encode(jr.data)
}

// BlobResponse represents a binary blob HTTP response (e.g., file download).
type BlobResponse struct {
	*BaseResponse
	Blob     []byte
	MimeType string
	FileName string
}

// InitBlobResponse creates a BlobResponse with content, MIME type, and filename.
func InitBlobResponse(blob []byte, mimeType, fileName string) BlobResponse {
	br := newBaseResponse()
	br.SetHeader("Content-Type", mimeType)
	br.SetHeader("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fileName))
	return BlobResponse{
		BaseResponse: br,
		Blob:         blob,
		MimeType:     mimeType,
		FileName:     fileName,
	}
}

// Write writes the blob content to the ResponseWriter.
// Value receiver ensures BlobResponse can be used as a Response.
func (br BlobResponse) Write(w http.ResponseWriter) {
	br.apply(w)
	w.Write(br.Blob)
}

// RedirectResponse represents an HTTP redirect response.
type RedirectResponse struct {
	*BaseResponse
	Location string
}

// InitRedirectResponse creates a redirect response with status code and Location header.
func InitRedirectResponse(location string, status int) RedirectResponse {
	rr := RedirectResponse{
		BaseResponse: newBaseResponse(),
		Location:     location,
	}
	rr.SetStatus(status)
	rr.SetHeader("Location", location)
	return rr
}

// Write sends the redirect status and header to the ResponseWriter.
// Value receiver ensures RedirectResponse can be used as a Response.
func (rr RedirectResponse) Write(w http.ResponseWriter) {
	rr.apply(w)
}
