package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHealth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("path = %q, want /healthz", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	got, err := New(srv.URL).Health(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "ok" {
		t.Fatalf("status = %q, want ok", got.Status)
	}
}

func TestReadyReturnsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"status":"unavailable"}`))
	}))
	defer srv.Close()

	err := New(srv.URL).Ready(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", apiErr.StatusCode)
	}
}
