package audit

import (
	"context"
	"log/slog"
)

// LogSink writes events as structured log lines. Your log pipeline
// (Loki, Datadog, CloudWatch...) routes them on stream=audit.
// Swap for a durable sink (Postgres, Kafka) when compliance asks for one.
type LogSink struct {
	log *slog.Logger
}

func NewLogSink(log *slog.Logger) *LogSink {
	return &LogSink{log: log.With("stream", "audit")}
}

func (s *LogSink) Record(ctx context.Context, e Event) {
	attrs := []slog.Attr{
		slog.Time("occurred_at", e.Time),
		slog.String("action", e.Action),
		slog.String("target", e.Target),
		slog.String("actor", e.Actor),
		slog.String("outcome", string(e.Outcome)),
	}
	if e.RequestID != "" {
		attrs = append(attrs, slog.String("request_id", e.RequestID))
	}
	if e.Error != "" {
		attrs = append(attrs, slog.String("error", e.Error))
	}
	if len(e.Details) > 0 {
		d := make([]any, 0, len(e.Details)*2)
		for k, v := range e.Details {
			d = append(d, k, v)
		}
		attrs = append(attrs, slog.Group("details", d...))
	}
	s.log.LogAttrs(ctx, slog.LevelInfo, "audit", attrs...)
}
