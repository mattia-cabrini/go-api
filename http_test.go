// Copyright (C) 2025-2026 Mattia Cabrini
// SPDX-License-Identifier: MIT

package goapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// setupDist builds a minimal dist tree: an index.html shell and one real asset.
func setupDist(t *testing.T) string {
	t.Helper()
	dist := t.TempDir()

	if err := os.WriteFile(filepath.Join(dist, "index.html"), []byte("INDEX"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dist, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "assets", "app.js"), []byte("APP"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dist
}

func doDist(t *testing.T, dist, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	handleDist(dist, InitURI(target), rec, req)
	return rec
}

func TestSPAFallbackServesIndexForRoute(t *testing.T) {
	dist := setupDist(t)
	rec := doDist(t, dist, "/settings/currencies")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "INDEX" {
		t.Errorf("body = %q, want INDEX (the SPA shell)", rec.Body.String())
	}
}

func TestSPAFallbackServesRootIndex(t *testing.T) {
	dist := setupDist(t)
	rec := doDist(t, dist, "/")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "INDEX" {
		t.Errorf("body = %q, want INDEX", rec.Body.String())
	}
}

func TestSPAServesRealAsset(t *testing.T) {
	dist := setupDist(t)
	rec := doDist(t, dist, "/assets/app.js")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "APP" {
		t.Errorf("body = %q, want APP", rec.Body.String())
	}
}

func TestSPAFallback404sMissingAsset(t *testing.T) {
	dist := setupDist(t)
	rec := doDist(t, dist, "/assets/missing.js")

	if rec.Code != http.StatusNotFound {
		t.Errorf("missing asset: status = %d, want 404 (must not be masked by index.html)", rec.Code)
	}
}
