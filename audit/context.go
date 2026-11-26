package audit

import "context"

// Request-scoped identity travels in the context. Whoever authenticates the
// caller (HTTP middleware today, a CLI tomorrow) sets it once; everything
// downstream reads it without knowing where it came from.

type ctxKey int

const (
	actorKey ctxKey = iota
	requestIDKey
)

func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorKey, actor)
}

// ActorFrom reports the authenticated actor, if any.
func ActorFrom(ctx context.Context) (string, bool) {
	a, ok := ctx.Value(actorKey).(string)
	return a, ok && a != ""
}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}
