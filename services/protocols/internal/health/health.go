// Package health exposes liveness/readiness endpoints (PLAN.md section 11.5).
package health

import "net/http"

// PingerFunc reports whether a dependency (e.g. Postgres) is reachable.
type PingerFunc func() error

// Handler serves /health/live (process up) and /health/ready (dependencies reachable).
func Handler(ping PingerFunc) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health/live", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/health/ready", func(w http.ResponseWriter, r *http.Request) {
		if err := ping(); err != nil {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	return mux
}
