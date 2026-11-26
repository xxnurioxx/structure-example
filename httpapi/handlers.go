package httpapi

import (
	"net/http"
	"strconv"

	"github.com/acme/kdeploy/deploy"
)

const (
	defaultLimit = 50
	maxLimit     = 500
)

// PUT is the only write verb: Server-Side Apply is an idempotent upsert,
// so POST-vs-PATCH-vs-PUT ceremony buys nothing. Body is the Spec;
// identity comes from the URL.
func (s *server) apply(w http.ResponseWriter, r *http.Request) error {
	var spec deploy.Spec
	if err := decode(w, r, &spec); err != nil {
		return err
	}
	d, err := s.deployer.Apply(r.Context(), deploy.Deployment{
		Namespace: r.PathValue("ns"),
		Name:      r.PathValue("name"),
		Spec:      spec,
	})
	if err != nil {
		return err
	}
	return respond(w, http.StatusOK, d)
}

func (s *server) get(w http.ResponseWriter, r *http.Request) error {
	d, err := s.deployer.Get(r.Context(), r.PathValue("ns"), r.PathValue("name"))
	if err != nil {
		return err
	}
	return respond(w, http.StatusOK, d)
}

func (s *server) list(w http.ResponseWriter, r *http.Request) error {
	limit, err := parseLimit(r.URL.Query().Get("limit"))
	if err != nil {
		return err
	}
	l, err := s.deployer.List(r.Context(), r.PathValue("ns"), deploy.Page{
		Limit:  limit,
		Cursor: r.URL.Query().Get("cursor"),
	})
	if err != nil {
		return err
	}
	return respond(w, http.StatusOK, l)
}

func (s *server) delete(w http.ResponseWriter, r *http.Request) error {
	if err := s.deployer.Delete(r.Context(), r.PathValue("ns"), r.PathValue("name")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusAccepted) // deletion is async in k8s; be honest about it
	return nil
}

func (s *server) ready(w http.ResponseWriter, r *http.Request) error {
	if s.opts.Ready != nil {
		if err := s.opts.Ready(r.Context()); err != nil {
			return httpErr(http.StatusServiceUnavailable, "not ready")
		}
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func parseLimit(v string) (int64, error) {
	if v == "" {
		return defaultLimit, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 1 || n > maxLimit {
		return 0, httpErr(http.StatusBadRequest, "limit must be an integer between 1 and "+strconv.Itoa(maxLimit))
	}
	return n, nil
}
