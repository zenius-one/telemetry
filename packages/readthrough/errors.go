package readthrough

import (
	"errors"
	"time"
)

var (
	// ErrNotFound is returned by a Loader to say the entity does not exist (it becomes a
	// tombstone), and by Get when a tombstone answers the lookup.
	ErrNotFound = errors.New("readthrough: not found")
	// ErrShed means the class's load budget is exhausted and the lookup was refused rather than
	// queued. Callers should answer 503 with the ShedError's RetryAfter.
	ErrShed = errors.New("readthrough: load shed")
	// ErrInvalidKey is returned for an empty, oversized or control-character key. Nothing is
	// cached or loaded for it.
	ErrInvalidKey = errors.New("readthrough: invalid key")
	// ErrUnknownClass is returned for a class that was not declared in New.
	ErrUnknownClass = errors.New("readthrough: unknown class")
)

// ShedError is the concrete error behind ErrShed.
type ShedError struct {
	Class      string
	RetryAfter time.Duration
}

func (e *ShedError) Error() string { return "readthrough: load shed for class " + e.Class }

// Is makes errors.Is(err, ErrShed) true.
func (e *ShedError) Is(target error) bool { return target == ErrShed }
