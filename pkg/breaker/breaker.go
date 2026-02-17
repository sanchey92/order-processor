package breaker

import (
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"
)

var ErrOpen = errors.New("breaker is open")

type State int32

const (
	Closed   State = 0
	Open     State = 1
	HalfOpen State = 3
)

func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case Open:
		return "open"
	case HalfOpen:
		return "half-open"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}

type Config struct {
	Name              string
	MaxFailures       int
	ResetTimeout      time.Duration
	HalfOpenSuccesses int
	SlowCallThreshold time.Duration
	IsFailure         func(err error) bool
}

type Option func(*Breaker)

func WithClock(now func() time.Time) Option {
	return func(b *Breaker) {
		b.now = now
	}
}

type snapshot struct {
	state        State
	failures     int
	successes    int
	generation   uint64
	lastFailedAt time.Time
}

type Breaker struct {
	cfg Config
	log *slog.Logger
	now func() time.Time

	snap    atomic.Pointer[snapshot]
	probing atomic.Bool // single-slot semaphore for half-open probes
}

func New(cfg Config, log *slog.Logger, opts ...Option) *Breaker {
	if cfg.MaxFailures <= 0 {
		cfg.MaxFailures = 5
	}
	if cfg.ResetTimeout <= 0 {
		cfg.ResetTimeout = 30 * time.Second
	}
	if cfg.HalfOpenSuccesses <= 0 {
		cfg.HalfOpenSuccesses = 3
	}
	if cfg.Name == "" {
		cfg.Name = "default-breaker"
	}

	b := &Breaker{
		cfg: cfg,
		log: log,
		now: time.Now,
	}

	for _, opt := range opts {
		opt(b)
	}

	b.snap.Store(&snapshot{state: Closed})
	return b
}
