package main

import (
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// config is 12-factor: env only, validated once at boot, fail fast.
type config struct {
	Addr            string
	Tokens          map[string]string // token → actor
	AuditSink       string            // "log" | "none"
	LogLevel        slog.Level
	ShutdownTimeout time.Duration
}

func loadConfig(getenv func(string) string) (config, error) {
	c := config{
		Addr:            or(getenv("KDEPLOY_ADDR"), ":8080"),
		AuditSink:       or(getenv("KDEPLOY_AUDIT_SINK"), "log"),
		ShutdownTimeout: 15 * time.Second,
	}

	tokens, err := parseTokens(getenv("KDEPLOY_TOKENS"))
	if err != nil {
		return c, fmt.Errorf("KDEPLOY_TOKENS: %w", err)
	}
	c.Tokens = tokens

	switch c.AuditSink {
	case "log", "none":
	default:
		return c, fmt.Errorf("KDEPLOY_AUDIT_SINK: unknown sink %q (want log or none)", c.AuditSink)
	}

	if v := getenv("KDEPLOY_LOG_LEVEL"); v != "" {
		if err := c.LogLevel.UnmarshalText([]byte(v)); err != nil {
			return c, fmt.Errorf("KDEPLOY_LOG_LEVEL: %w", err)
		}
	}
	if v := getenv("KDEPLOY_SHUTDOWN_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return c, fmt.Errorf("KDEPLOY_SHUTDOWN_TIMEOUT: %w", err)
		}
		c.ShutdownTimeout = d
	}
	return c, nil
}

// parseTokens reads "alice=tok1,ci=tok2". Named tokens are what make the
// audit log answer "who"; a single shared token can only ever say "someone".
func parseTokens(v string) (map[string]string, error) {
	out := map[string]string{}
	if v == "" {
		return out, nil
	}
	for pair := range strings.SplitSeq(v, ",") {
		actor, tok, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || actor == "" || tok == "" {
			return nil, fmt.Errorf("want actor=token pairs, got %q", pair)
		}
		if _, dup := out[tok]; dup {
			return nil, fmt.Errorf("token for %q is not unique", actor)
		}
		out[tok] = actor
	}
	return out, nil
}

func or(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
