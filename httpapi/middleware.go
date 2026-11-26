package httpapi

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/acme/kdeploy/audit"
)

// Middleware is plain func(http.Handler) http.Handler. No framework, no magic.

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" || len(id) > 128 {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(audit.WithRequestID(r.Context(), id)))
	})
}

func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" {
			return // probes are noise
		}
		log.InfoContext(r.Context(), "http",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"dur", time.Since(start),
			"request_id", audit.RequestIDFrom(r.Context()),
		)
	})
}

func recoverPanics(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				log.ErrorContext(r.Context(), "panic", "value", v, "stack", string(debug.Stack()),
					"request_id", audit.RequestIDFrom(r.Context()))
				http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// Auth is split in two so audit can sit between them:
//
//	authenticate → auditRequests → requireAuth → handlers
//
// That way rejected requests (401s) are audited too. They're the ones
// security actually wants to see.

// authenticate resolves the bearer token to an actor name, if it matches.
// It never rejects; that's requireAuth's job.
func authenticate(tokens map[string]string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			if actor := lookupToken(tokens, got); actor != "" {
				r = r.WithContext(audit.WithActor(r.Context(), actor))
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requireAuth rejects requests without an actor. With no tokens configured
// auth is disabled (main logs a warning about it at boot).
func requireAuth(enabled bool, next http.Handler) http.Handler {
	if !enabled {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := audit.ActorFrom(r.Context()); !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="kdeploy"`)
			http.Error(w, http.StatusText(http.StatusUnauthorized), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// lookupToken compares against every token in constant time so response
// timing doesn't leak which prefix, or which token, was close.
func lookupToken(tokens map[string]string, got string) string {
	var actor string
	for tok, name := range tokens {
		if subtle.ConstantTimeCompare([]byte(got), []byte(tok)) == 1 {
			actor = name
		}
	}
	return actor
}

// auditRequests records every API request: who, what, and the status we answered.
func auditRequests(rec audit.Recorder, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sr := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sr, r)

		e := audit.New(r.Context(), "http.request", r.URL.Path)
		if sr.status >= 400 {
			e.Outcome, e.Error = audit.Failure, http.StatusText(sr.status)
		}
		e.Details = map[string]any{"method": r.Method, "status": sr.status}
		rec.Record(r.Context(), e)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the underlying writer (flush, deadlines).
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
