// Package notifier defines the Notifier interface and Payload type used to
// forward Claude Code hook events to external notification CLIs.
package notifier

import "context"

// Payload holds the notification fields extracted from a /notify request body.
// All fields have already been trimmed and length-capped by the HTTP handler.
type Payload struct {
	Title       string
	Subtitle    string
	Body        string
	WorkspaceID string
	SurfaceID   string
	Source      string
	Kind        string
}

// Notifier is implemented by any notification backend. The single method
// Notify delivers p using whatever mechanism the concrete type provides.
type Notifier interface {
	Notify(ctx context.Context, p Payload) error
}

// ErrBinaryNotFound is returned by CmuxNotifier when the cmux binary cannot
// be located via any resolution strategy.
type ErrBinaryNotFound struct {
	Msg string
}

func (e *ErrBinaryNotFound) Error() string { return e.Msg }

// ErrTimeout is returned when the cmux command does not complete within the
// configured deadline.
type ErrTimeout struct{}

func (e *ErrTimeout) Error() string { return "cmux command timed out" }
