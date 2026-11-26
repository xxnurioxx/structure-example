package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/acme/kdeploy/audit"
	"github.com/acme/kdeploy/deploy"
)

const maxBody = 1 << 20 // 1 MiB

// handlerFunc returns errors instead of writing them. One place decides
// how errors become HTTP responses; handlers stay linear and boring.
type handlerFunc func(http.ResponseWriter, *http.Request) error

func (s *server) handle(h handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			s.writeError(w, r, err)
		}
	})
}

// problem is an RFC 9457 (application/problem+json) body.
type problem struct {
	Title     string            `json:"title"`
	Status    int               `json:"status"`
	Detail    string            `json:"detail,omitempty"`
	Fields    map[string]string `json:"fields,omitempty"`
	RequestID string            `json:"requestId,omitempty"`
}

type statusError struct {
	status int
	msg    string
}

func (e *statusError) Error() string { return e.msg }

func httpErr(status int, msg string) error { return &statusError{status, msg} }

func (s *server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	p := problem{Status: http.StatusInternalServerError, RequestID: audit.RequestIDFrom(r.Context())}

	var (
		se *statusError
		ve *deploy.ValidationError
	)
	switch {
	case errors.As(err, &se):
		p.Status, p.Detail = se.status, se.msg
	case errors.As(err, &ve):
		p.Status, p.Fields = http.StatusUnprocessableEntity, ve.Fields
	case errors.Is(err, deploy.ErrInvalid):
		p.Status, p.Detail = http.StatusUnprocessableEntity, err.Error()
	case errors.Is(err, deploy.ErrNotFound):
		p.Status = http.StatusNotFound
	case errors.Is(err, deploy.ErrConflict):
		p.Status, p.Detail = http.StatusConflict, err.Error()
	case errors.Is(err, r.Context().Err()):
		return // client went away; nothing to say
	default:
		// Never leak internals. Log loudly, answer vaguely.
		s.log.ErrorContext(r.Context(), "request failed", "err", err, "request_id", p.RequestID)
	}
	p.Title = http.StatusText(p.Status)

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}

func respond(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	return json.NewEncoder(w).Encode(v)
}

func decode(w http.ResponseWriter, r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields() // typos should fail, not silently no-op

	if err := dec.Decode(v); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return httpErr(http.StatusRequestEntityTooLarge, "body too large")
		}
		return httpErr(http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
	}
	if dec.More() {
		return httpErr(http.StatusBadRequest, "body must contain a single JSON object")
	}
	return nil
}
