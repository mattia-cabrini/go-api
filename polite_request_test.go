// Copyright (C) 2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package goapi

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func postForm(t *testing.T, body string) PoliteRequest {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/Api/X", strings.NewReader(body))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return initPoliteRequest(r)
}

func TestParamsPassThroughWithoutTransform(t *testing.T) {
	SetTransform(nil)

	m, err := postForm(t, "splitId=7&note=hi").Params()
	if err != nil {
		t.Fatal(err)
	}
	if m["splitId"] != "7" || m["note"] != "hi" {
		t.Errorf("params altered without a transform: %v", m)
	}
}

func TestParamsApplyTransform(t *testing.T) {
	SetTransform(func(p map[string]string) (map[string]interface{}, error) {
		m := make(map[string]interface{}, len(p))
		for k, v := range p {
			m[k] = v
		}
		if id, ok := p["splitId"]; ok {
			n, err := strconv.Atoi(id)
			if err != nil {
				return nil, errors.New("splitId must be an integer")
			}
			m["splitId"] = n
		}
		return m, nil
	})
	defer SetTransform(nil)

	m, err := postForm(t, "splitId=7&note=hi").Params()
	if err != nil {
		t.Fatal(err)
	}
	if m["splitId"] != 7 || m["note"] != "hi" {
		t.Errorf("transform not applied: %v", m)
	}

	if _, err = postForm(t, "splitId=seven").Params(); err == nil {
		t.Errorf("malformed parameter accepted")
	}
}
