// Command kdeploy serves a small HTTP API for managing Kubernetes Deployments.
//
// main is the only place that knows about env vars, signals and wiring.
// Everything below it receives its dependencies explicitly: no globals, no init().
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/acme/kdeploy/audit"
	"github.com/acme/kdeploy/deploy"
	"github.com/acme/kdeploy/httpapi"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Getenv); err != nil {
		fmt.Fprintln(os.Stderr, "kdeploy:", err)
		os.Exit(1)
	}
}

// run is main, but testable and with an error return.
func run(ctx context.Context, getenv func(string) string) error {
	cfg, err := loadConfig(getenv)
	if err != nil {
		return err
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	client, err := newKubeClient()
	if err != nil {
		return fmt.Errorf("kube client: %w", err)
	}
	if len(cfg.Tokens) == 0 {
		log.Warn("KDEPLOY_TOKENS not set: API is unauthenticated, audit actor will be \"anonymous\"")
	}

	// Composition root: this is the only place that knows the pieces fit together.
	rec := newAuditRecorder(cfg.AuditSink, log)
	kube := deploy.NewKube(client)
	deployer := deploy.NewAudited(kube, rec)

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: httpapi.New(log, deployer, httpapi.Options{
			Tokens: cfg.Tokens,
			Ready:  kube.Ping, // readiness isn't a user action; bypass audit
			Audit:  rec,
		}),
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	log.Info("shutting down", "timeout", cfg.ShutdownTimeout)
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func newAuditRecorder(sink string, log *slog.Logger) audit.Recorder {
	switch sink {
	case "none":
		return audit.Discard
	default: // "log"; validated in loadConfig
		return audit.NewLogSink(log)
	}
}

// newKubeClient follows kubectl's rules: $KUBECONFIG, ~/.kube/config,
// then in-cluster service account. Same binary works on a laptop and in a pod.
func newKubeClient() (kubernetes.Interface, error) {
	rc, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		&clientcmd.ConfigOverrides{},
	).ClientConfig()
	if err != nil {
		return nil, err
	}
	rc.UserAgent = "kdeploy"
	rc.QPS, rc.Burst = 50, 100
	return kubernetes.NewForConfig(rc)
}
