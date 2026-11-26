// Package httpapi is the HTTP transport. It knows nothing about Kubernetes:
// it speaks to whatever satisfies Deployer.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/acme/kdeploy/audit"
	"github.com/acme/kdeploy/deploy"
)

// Deployer is declared here, by the consumer, with exactly the methods we use.
// deploy.Kube satisfies it implicitly; a fake for local dev would too.
type Deployer interface {
	Apply(ctx context.Context, d deploy.Deployment) (deploy.Deployment, error)
	Get(ctx context.Context, ns, name string) (deploy.Deployment, error)
	List(ctx context.Context, ns string, p deploy.Page) (deploy.List, error)
	Delete(ctx context.Context, ns, name string) error
}

type Options struct {
	Tokens map[string]string               // bearer token → actor name; empty disables auth
	Ready  func(ctx context.Context) error // readiness check; nil means always ready
	Audit  audit.Recorder                  // nil means audit.Discard
}

type server struct {
	log      *slog.Logger
	deployer Deployer
	opts     Options
}

// New returns the fully wired http.Handler. Routing uses the stdlib mux
// (Go 1.22+ method + wildcard patterns): no framework needed.
func New(log *slog.Logger, d Deployer, opts Options) http.Handler {
	if opts.Audit == nil {
		opts.Audit = audit.Discard
	}
	s := &server{log: log, deployer: d, opts: opts}

	api := http.NewServeMux()
	api.Handle("GET /v1/namespaces/{ns}/deployments", s.handle(s.list))
	api.Handle("GET /v1/namespaces/{ns}/deployments/{name}", s.handle(s.get))
	api.Handle("PUT /v1/namespaces/{ns}/deployments/{name}", s.handle(s.apply))
	api.Handle("DELETE /v1/namespaces/{ns}/deployments/{name}", s.handle(s.delete))

	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	root.Handle("GET /readyz", s.handle(s.ready))
	// Probes stay outside auth and audit; the API goes through both.
	root.Handle("/v1/", authenticate(opts.Tokens,
		auditRequests(opts.Audit,
			requireAuth(len(opts.Tokens) > 0, api))))

	return requestID(logRequests(log, recoverPanics(log, root)))
}
