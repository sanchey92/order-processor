package breaker

import (
	"context"
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
	cfg *Config
	log *slog.Logger
	now func() time.Time

	snap    atomic.Pointer[snapshot]
	probing atomic.Bool // single-slot semaphore for half-open probes
}

func New(cfg *Config, log *slog.Logger, opts ...Option) *Breaker {
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

func (b *Breaker) Execute(ctx context.Context, fn func(context.Context) error) error {
	isProbe, err := b.allow()
	if err != nil {
		return err
	}

	defer func() {
		if isProbe {
			b.probing.Store(false)
		}
	}()

	panicked := true
	start := b.now()

	defer func() {
		if panicked {
			b.recordFailure()
		}
	}()

	err = fn(ctx)
	panicked = false
	elapsed := b.now().Sub(start)

	if err != nil && errors.Is(ctx.Err(), context.Canceled) && errors.Is(err, context.Canceled) {
		return err
	}

	if b.isFailure(err, elapsed) {
		b.recordFailure()
	} else {
		b.recordSuccess()
	}

	return err
}

func (b *Breaker) GetState() State {
	return b.snap.Load().state
}

func (b *Breaker) Counts() (failures, successes int) {
	s := b.snap.Load()
	return s.failures, s.successes
}

func (b *Breaker) Reset() {
	for {
		cur := b.snap.Load()
		next := &snapshot{
			state:      Closed,
			generation: cur.generation + 1,
		}
		if b.snap.CompareAndSwap(cur, next) {
			b.log.Info("breaker reset", slog.String("name", b.cfg.Name))
			return
		}
	}
}

func (b *Breaker) allow() (bool, error) {
	for {
		cur := b.snap.Load()

		switch cur.state {
		case Closed:
			return false, nil

		case Open:
			if b.now().Sub(cur.lastFailedAt) < b.cfg.ResetTimeout {
				return false, ErrOpen
			}
			if !b.probing.CompareAndSwap(false, true) {
				return false, ErrOpen
			}
			next := &snapshot{
				state:      HalfOpen,
				generation: cur.generation + 1,
			}
			if b.snap.CompareAndSwap(cur, next) {
				b.log.Info("breaker half-open", slog.String("name", b.cfg.Name))
				return true, nil
			}
			b.probing.Store(false)
			continue

		case HalfOpen:
			if !b.probing.CompareAndSwap(false, true) {
				return false, ErrOpen
			}

			if b.snap.Load().state != HalfOpen {
				b.probing.Store(false)
				continue
			}
			return true, nil

		default:
			return false, ErrOpen
		}
	}
}

func (b *Breaker) isFailure(err error, elapsed time.Duration) bool {
	if b.cfg.SlowCallThreshold > 0 && elapsed >= b.cfg.SlowCallThreshold {
		return true
	}
	if err == nil {
		return false
	}
	if b.cfg.IsFailure != nil {
		return b.cfg.IsFailure(err)
	}
	return true
}

func (b *Breaker) recordSuccess() {
	for {
		cur := b.snap.Load()

		switch cur.state {
		case Closed:
			if cur.failures == 0 {
				return
			}
			next := &snapshot{
				state:      Closed,
				generation: cur.generation,
			}
			if b.snap.CompareAndSwap(cur, next) {
				return
			}

		case HalfOpen:
			successes := cur.successes + 1
			if successes >= b.cfg.HalfOpenSuccesses {
				next := &snapshot{
					state:      Closed,
					generation: cur.generation + 1,
				}
				if b.snap.CompareAndSwap(cur, next) {
					b.log.Info("breaker closed",
						slog.String("name", b.cfg.Name),
						slog.Int("after_successes", successes),
					)

					return
				}
			} else {
				next := &snapshot{
					state:      HalfOpen,
					successes:  successes,
					generation: cur.generation,
				}
				if b.snap.CompareAndSwap(cur, next) {
					return
				}
			}

		default:
			return
		}
	}
}

func (b *Breaker) recordFailure() {
	for {
		cur := b.snap.Load()

		switch cur.state {
		case Closed:
			failures := cur.failures + 1
			if failures >= b.cfg.MaxFailures {
				next := &snapshot{
					state:        Open,
					failures:     failures,
					generation:   cur.generation + 1,
					lastFailedAt: b.now(),
				}
				if b.snap.CompareAndSwap(cur, next) {
					b.log.Warn("breaker opened",
						slog.String("name", b.cfg.Name),
						slog.Int("failures", failures),
					)
					return
				}
			} else {
				next := &snapshot{
					state:      Closed,
					failures:   failures,
					generation: cur.generation,
				}
				if b.snap.CompareAndSwap(cur, next) {
					return
				}
			}

		case HalfOpen:
			next := &snapshot{
				state:        Open,
				failures:     1,
				generation:   cur.generation + 1,
				lastFailedAt: b.now(),
			}
			if b.snap.CompareAndSwap(cur, next) {
				b.log.Warn("breaker re-opened from half-opened",
					slog.String("name", b.cfg.Name),
				)
				return
			}

		default:
			return
		}
	}
}
