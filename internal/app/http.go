package app

import (
	"context"
	"io"
	"net/http"
	"time"

	"tadmin/internal/store"
)

const maximumRequestBodyBytes = 1 << 20

func newHTTPHandler(database *store.Store, verification http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, `{"status":"method_not_allowed"}`, http.StatusMethodNotAllowed)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		if err := database.Ping(ctx); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, "{\"status\":\"unhealthy\"}\n")
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "{\"status\":\"ok\"}\n")
	})
	if verification != nil {
		mux.Handle("/verify/", verification)
		mux.Handle("/api/verify/", verification)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maximumRequestBodyBytes)
		mux.ServeHTTP(w, r)
	})
}
