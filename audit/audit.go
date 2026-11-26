// Package audit records who did what, to which target, and whether it worked.
//
// It is a leaf package: it imports nothing from this module, so any
// package (httpapi, deploy, a future CLI) can report into it without cycles.
package audit

import (
	"context"
	"time"
)

type Outcome string

const (
	Success Outcome = "success"
	Failure Outcome = "failure"
)

type Event struct {
	Time      time.Time
	Action    string // e.g. "http.request", "deployment.apply"
	Target    string // e.g. "default/web"
	Actor     string
	RequestID string
	Outcome   Outcome
	Error     string
	Details   map[string]any
}

// Recorder is defined here, next to its implementations, on purpose:
// like io.Writer, it is one small contract with many sinks (log, DB, queue).
//
// Record is best effort by design: it never fails the caller's operation.
// Sinks are responsible for reporting their own delivery failures.
type Recorder interface {
	Record(ctx context.Context, e Event)
}

// New starts an Event, pulling actor and request ID from ctx so callers
// never have to thread them through by hand.
func New(ctx context.Context, action, target string) Event {
	actor, ok := ActorFrom(ctx)
	if !ok {
		actor = "anonymous"
	}
	return Event{
		Time:      time.Now().UTC(),
		Action:    action,
		Target:    target,
		Actor:     actor,
		RequestID: RequestIDFrom(ctx),
		Outcome:   Success,
	}
}

// Result marks the event as failed if err is non-nil.
func (e Event) Result(err error) Event {
	if err != nil {
		e.Outcome, e.Error = Failure, err.Error()
	}
	return e
}

// Discard drops every event. Useful for local dev or when audit is disabled.
var Discard Recorder = discard{}

type discard struct{}

func (discard) Record(context.Context, Event) {}
