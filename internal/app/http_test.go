package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"tadmin/internal/store"
)

func TestHealthz(t *testing.T) {
	database, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "tadmin.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer database.Close()

	handler := newHTTPHandler(database, nil)
	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if response.Body.String() != "{\"status\":\"ok\"}\n" {
		t.Errorf("body = %q", response.Body.String())
	}
	for _, sensitive := range []string{"database_path", "token", "tadmin.db"} {
		if strings.Contains(strings.ToLower(response.Body.String()), sensitive) {
			t.Errorf("health response contains sensitive value %q", sensitive)
		}
	}
}

func TestHealthzRejectsOtherMethods(t *testing.T) {
	database, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "tadmin.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer database.Close()

	request := httptest.NewRequest(http.MethodPost, "/healthz", strings.NewReader("ignored"))
	response := httptest.NewRecorder()
	newHTTPHandler(database, nil).ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", response.Code, http.StatusMethodNotAllowed)
	}
	if response.Header().Get("Allow") != http.MethodGet {
		t.Errorf("Allow header = %q", response.Header().Get("Allow"))
	}
}

func TestHealthzReportsUnavailableDatabase(t *testing.T) {
	database, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "tadmin.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	handler := newHTTPHandler(database, nil)
	if err := database.Close(); err != nil {
		t.Fatalf("database.Close() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if response.Body.String() != "{\"status\":\"unhealthy\"}\n" {
		t.Errorf("body = %q", response.Body.String())
	}
}
