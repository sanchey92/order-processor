package breaker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var errTest = errors.New("test error")

func newTestBreaker(cfg *Config, opts ...Option) *Breaker {
	log := slog.Default()
	now := time.Now()
	clock := func() time.Time { return now }
	opts = append([]Option{WithClock(clock)}, opts...)
	return New(cfg, log, opts...)
}

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Now()}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newBreakerWithClock(cfg *Config, clock *fakeClock) *Breaker {
	return New(cfg, slog.Default(), WithClock(clock.Now))
}

// --- Initial state ---

func TestNewBreaker_StartsInClosedState(t *testing.T) {
	b := newTestBreaker(&Config{})
	if b.GetState() != Closed {
		t.Fatalf("expected Closed, got %s", b.GetState())
	}
}

func TestNewBreaker_ZeroCounters(t *testing.T) {
	b := newTestBreaker(&Config{})
	f, s := b.Counts()
	if f != 0 || s != 0 {
		t.Fatalf("expected (0,0), got (%d,%d)", f, s)
	}
}

// --- Config defaults ---

func TestConfigDefaults(t *testing.T) {
	cfg := &Config{}
	_ = newTestBreaker(cfg)

	if cfg.MaxFailures != 5 {
		t.Errorf("MaxFailures default: got %d, want 5", cfg.MaxFailures)
	}
	if cfg.ResetTimeout != 30*time.Second {
		t.Errorf("ResetTimeout default: got %v, want 30s", cfg.ResetTimeout)
	}
	if cfg.HalfOpenSuccesses != 3 {
		t.Errorf("HalfOpenSuccesses default: got %d, want 3", cfg.HalfOpenSuccesses)
	}
}

// --- Closed state behavior ---

func TestClosed_SuccessfulCallsPassThrough(t *testing.T) {
	b := newTestBreaker(&Config{MaxFailures: 3})
	called := false
	err := b.Execute(context.Background(), func(ctx context.Context) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Fatal("fn was not called")
	}
}

func TestClosed_FailuresBelowThreshold_StaysClosed(t *testing.T) {
	b := newTestBreaker(&Config{MaxFailures: 3})

	for i := 0; i < 2; i++ {
		_ = b.Execute(context.Background(), func(ctx context.Context) error {
			return errTest
		})
	}

	if b.GetState() != Closed {
		t.Fatalf("expected Closed after %d failures, got %s", 2, b.GetState())
	}
	f, _ := b.Counts()
	if f != 2 {
		t.Fatalf("expected 2 failures, got %d", f)
	}
}

func TestClosed_ReturnsOriginalError(t *testing.T) {
	b := newTestBreaker(&Config{MaxFailures: 5})
	err := b.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})
	if !errors.Is(err, errTest) {
		t.Fatalf("expected errTest, got %v", err)
	}
}

// --- Closed → Open transition ---

func TestClosedToOpen_AfterMaxFailures(t *testing.T) {
	b := newTestBreaker(&Config{MaxFailures: 3})

	for i := 0; i < 3; i++ {
		_ = b.Execute(context.Background(), func(ctx context.Context) error {
			return errTest
		})
	}

	if b.GetState() != Open {
		t.Fatalf("expected Open after MaxFailures, got %s", b.GetState())
	}
}

func TestClosedToOpen_ExactlyAtThreshold(t *testing.T) {
	b := newTestBreaker(&Config{MaxFailures: 1})

	_ = b.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})

	if b.GetState() != Open {
		t.Fatalf("expected Open, got %s", b.GetState())
	}
}

// --- Open state behavior ---

func TestOpen_RejectsWithErrOpen(t *testing.T) {
	clock := newFakeClock()
	b := newBreakerWithClock(&Config{MaxFailures: 1, ResetTimeout: 10 * time.Second}, clock)

	_ = b.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})

	err := b.Execute(context.Background(), func(ctx context.Context) error {
		t.Fatal("fn should not be called in Open state")
		return nil
	})
	if !errors.Is(err, ErrOpen) {
		t.Fatalf("expected ErrOpen, got %v", err)
	}
}

func TestOpen_RejectsUntilTimeoutElapsed(t *testing.T) {
	clock := newFakeClock()
	b := newBreakerWithClock(&Config{MaxFailures: 1, ResetTimeout: 10 * time.Second}, clock)

	_ = b.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})

	clock.Advance(9 * time.Second)

	err := b.Execute(context.Background(), func(ctx context.Context) error {
		t.Fatal("should not be called")
		return nil
	})
	if !errors.Is(err, ErrOpen) {
		t.Fatalf("expected ErrOpen before timeout, got %v", err)
	}
}

// --- Open → HalfOpen transition ---

func TestOpenToHalfOpen_AfterResetTimeout(t *testing.T) {
	clock := newFakeClock()
	b := newBreakerWithClock(&Config{
		MaxFailures:       1,
		ResetTimeout:      10 * time.Second,
		HalfOpenSuccesses: 1,
	}, clock)

	_ = b.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})

	clock.Advance(11 * time.Second)

	// The probe call should go through (HalfOpen allows one probe)
	called := false
	_ = b.Execute(context.Background(), func(ctx context.Context) error {
		called = true
		return nil
	})

	if !called {
		t.Fatal("probe fn was not called after ResetTimeout")
	}
}

// --- HalfOpen → Closed transition ---

func TestHalfOpenToClosed_AfterSuccessThreshold(t *testing.T) {
	clock := newFakeClock()
	threshold := 3
	b := newBreakerWithClock(&Config{
		MaxFailures:       1,
		ResetTimeout:      10 * time.Second,
		HalfOpenSuccesses: threshold,
	}, clock)

	// Open the breaker
	_ = b.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})

	clock.Advance(11 * time.Second)

	// Deliver exactly HalfOpenSuccesses successful probes
	for i := 0; i < threshold; i++ {
		err := b.Execute(context.Background(), func(ctx context.Context) error {
			return nil
		})
		if err != nil {
			t.Fatalf("probe %d: unexpected error: %v", i+1, err)
		}
	}

	if b.GetState() != Closed {
		t.Fatalf("expected Closed after %d successes, got %s", threshold, b.GetState())
	}
}

func TestHalfOpen_StaysHalfOpenBeforeThreshold(t *testing.T) {
	clock := newFakeClock()
	threshold := 3
	b := newBreakerWithClock(&Config{
		MaxFailures:       1,
		ResetTimeout:      10 * time.Second,
		HalfOpenSuccesses: threshold,
	}, clock)

	_ = b.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})

	clock.Advance(11 * time.Second)

	// Only threshold-1 successes — should still be HalfOpen
	for i := 0; i < threshold-1; i++ {
		err := b.Execute(context.Background(), func(ctx context.Context) error {
			return nil
		})
		if err != nil {
			t.Fatalf("probe %d: unexpected error: %v", i+1, err)
		}
	}

	if b.GetState() != HalfOpen {
		t.Fatalf("expected HalfOpen after %d successes (threshold=%d), got %s",
			threshold-1, threshold, b.GetState())
	}
}

// --- HalfOpen → Open transition ---

func TestHalfOpenToOpen_OnFailure(t *testing.T) {
	clock := newFakeClock()
	b := newBreakerWithClock(&Config{
		MaxFailures:       1,
		ResetTimeout:      10 * time.Second,
		HalfOpenSuccesses: 3,
	}, clock)

	_ = b.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})

	clock.Advance(11 * time.Second)

	// Probe fails
	_ = b.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})

	if b.GetState() != Open {
		t.Fatalf("expected Open after HalfOpen failure, got %s", b.GetState())
	}
}

func TestHalfOpenToOpen_RejectsAfterFailedProbe(t *testing.T) {
	clock := newFakeClock()
	b := newBreakerWithClock(&Config{
		MaxFailures:       1,
		ResetTimeout:      10 * time.Second,
		HalfOpenSuccesses: 3,
	}, clock)

	_ = b.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})

	clock.Advance(11 * time.Second)

	_ = b.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})

	// Should be Open again, rejecting calls
	err := b.Execute(context.Background(), func(ctx context.Context) error {
		t.Fatal("should not be called")
		return nil
	})
	if !errors.Is(err, ErrOpen) {
		t.Fatalf("expected ErrOpen, got %v", err)
	}
}

// --- HalfOpen single-probe ---

func TestHalfOpen_SingleProbe_OthersGetErrOpen(t *testing.T) {
	clock := newFakeClock()
	b := newBreakerWithClock(&Config{
		MaxFailures:       1,
		ResetTimeout:      10 * time.Second,
		HalfOpenSuccesses: 3,
	}, clock)

	_ = b.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})

	clock.Advance(11 * time.Second)

	probeCh := make(chan struct{})
	doneCh := make(chan struct{})

	// First goroutine: probe that blocks
	go func() {
		defer close(doneCh)
		_ = b.Execute(context.Background(), func(ctx context.Context) error {
			probeCh <- struct{}{} // signal: probe is running
			<-probeCh             // wait to be released
			return nil
		})
	}()

	<-probeCh // wait for probe to be running

	// Second goroutine tries concurrently — should be rejected
	err := b.Execute(context.Background(), func(ctx context.Context) error {
		t.Fatal("second caller should not execute during probe")
		return nil
	})
	if !errors.Is(err, ErrOpen) {
		t.Fatalf("expected ErrOpen for concurrent call during probe, got %v", err)
	}

	probeCh <- struct{}{} // release probe
	<-doneCh
}

// --- Near-consecutive model ---

func TestClosed_OneSuccessResetsFailures(t *testing.T) {
	b := newTestBreaker(&Config{MaxFailures: 3})

	// 2 failures
	for i := 0; i < 2; i++ {
		_ = b.Execute(context.Background(), func(ctx context.Context) error {
			return errTest
		})
	}

	f, _ := b.Counts()
	if f != 2 {
		t.Fatalf("expected 2 failures, got %d", f)
	}

	// 1 success resets
	_ = b.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})

	f, _ = b.Counts()
	if f != 0 {
		t.Fatalf("expected 0 failures after success, got %d", f)
	}

	// Need MaxFailures again from scratch to open
	for i := 0; i < 2; i++ {
		_ = b.Execute(context.Background(), func(ctx context.Context) error {
			return errTest
		})
	}
	if b.GetState() != Closed {
		t.Fatalf("expected Closed (only 2 failures after reset), got %s", b.GetState())
	}
}

// --- Panic handling ---

func TestPanic_RecordedAsFailureAndRePropagated(t *testing.T) {
	b := newTestBreaker(&Config{MaxFailures: 2})

	func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Fatal("expected panic to propagate")
			}
			if r != "boom" {
				t.Fatalf("expected panic value 'boom', got %v", r)
			}
		}()
		_ = b.Execute(context.Background(), func(ctx context.Context) error {
			panic("boom")
		})
	}()

	f, _ := b.Counts()
	if f != 1 {
		t.Fatalf("expected 1 failure after panic, got %d", f)
	}
	if b.GetState() != Closed {
		t.Fatalf("expected Closed (1 failure < MaxFailures=2), got %s", b.GetState())
	}
}

func TestPanic_OpensBreaker_WhenAtThreshold(t *testing.T) {
	b := newTestBreaker(&Config{MaxFailures: 1})

	func() {
		defer func() { recover() }()
		_ = b.Execute(context.Background(), func(ctx context.Context) error {
			panic("boom")
		})
	}()

	if b.GetState() != Open {
		t.Fatalf("expected Open after panic at MaxFailures=1, got %s", b.GetState())
	}
}

// --- Slow calls ---

func TestSlowCall_CountedAsFailure(t *testing.T) {
	clock := newFakeClock()
	b := newBreakerWithClock(&Config{
		MaxFailures:       2,
		SlowCallThreshold: 5 * time.Second,
	}, clock)

	_ = b.Execute(context.Background(), func(ctx context.Context) error {
		clock.Advance(6 * time.Second) // slower than threshold
		return nil                     // no error, but slow
	})

	f, _ := b.Counts()
	if f != 1 {
		t.Fatalf("expected 1 failure for slow call, got %d", f)
	}
}

func TestSlowCall_ExactThreshold_IsFailure(t *testing.T) {
	clock := newFakeClock()
	b := newBreakerWithClock(&Config{
		MaxFailures:       2,
		SlowCallThreshold: 5 * time.Second,
	}, clock)

	_ = b.Execute(context.Background(), func(ctx context.Context) error {
		clock.Advance(5 * time.Second) // exactly at threshold
		return nil
	})

	f, _ := b.Counts()
	if f != 1 {
		t.Fatalf("expected 1 failure for call at exact threshold, got %d", f)
	}
}

func TestSlowCall_BelowThreshold_NotFailure(t *testing.T) {
	clock := newFakeClock()
	b := newBreakerWithClock(&Config{
		MaxFailures:       2,
		SlowCallThreshold: 5 * time.Second,
	}, clock)

	_ = b.Execute(context.Background(), func(ctx context.Context) error {
		clock.Advance(4 * time.Second) // below threshold
		return nil
	})

	f, _ := b.Counts()
	if f != 0 {
		t.Fatalf("expected 0 failures for fast call, got %d", f)
	}
}

// --- context.Canceled ---

func TestContextCanceled_Neutral(t *testing.T) {
	b := newTestBreaker(&Config{MaxFailures: 3})

	ctx, cancel := context.WithCancel(context.Background())

	err := b.Execute(ctx, func(ctx context.Context) error {
		cancel()
		return ctx.Err()
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	f, s := b.Counts()
	if f != 0 || s != 0 {
		t.Fatalf("expected neutral (0,0) after context.Canceled, got (%d,%d)", f, s)
	}
}

// --- context.DeadlineExceeded ---

func TestContextDeadlineExceeded_IsFailure(t *testing.T) {
	b := newTestBreaker(&Config{MaxFailures: 3})

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	time.Sleep(1 * time.Millisecond)

	_ = b.Execute(ctx, func(ctx context.Context) error {
		return ctx.Err()
	})

	f, _ := b.Counts()
	if f != 1 {
		t.Fatalf("expected 1 failure for DeadlineExceeded, got %d", f)
	}
}

// --- Custom IsFailure ---

func TestCustomIsFailure_OnlyMatchingErrorsCounted(t *testing.T) {
	var errCritical = errors.New("critical")
	var errIgnorable = errors.New("ignorable")

	b := newTestBreaker(&Config{
		MaxFailures: 2,
		IsFailure: func(err error) bool {
			return errors.Is(err, errCritical)
		},
	})

	// Ignorable error — should NOT count as failure
	_ = b.Execute(context.Background(), func(ctx context.Context) error {
		return errIgnorable
	})
	f, _ := b.Counts()
	if f != 0 {
		t.Fatalf("expected 0 failures for ignorable error, got %d", f)
	}

	// Critical error — should count
	_ = b.Execute(context.Background(), func(ctx context.Context) error {
		return errCritical
	})
	f, _ = b.Counts()
	if f != 1 {
		t.Fatalf("expected 1 failure for critical error, got %d", f)
	}
}

// --- Reset ---

func TestReset_ReturnsToClosed(t *testing.T) {
	b := newTestBreaker(&Config{MaxFailures: 1})

	_ = b.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})

	if b.GetState() != Open {
		t.Fatalf("expected Open, got %s", b.GetState())
	}

	b.Reset()

	if b.GetState() != Closed {
		t.Fatalf("expected Closed after Reset, got %s", b.GetState())
	}
	f, s := b.Counts()
	if f != 0 || s != 0 {
		t.Fatalf("expected (0,0) after Reset, got (%d,%d)", f, s)
	}
}

func TestReset_AllowsCallsAgain(t *testing.T) {
	b := newTestBreaker(&Config{MaxFailures: 1})

	_ = b.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})

	b.Reset()

	called := false
	err := b.Execute(context.Background(), func(ctx context.Context) error {
		called = true
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error after Reset: %v", err)
	}
	if !called {
		t.Fatal("fn not called after Reset")
	}
}

// --- Full cycle ---

func TestFullCycle_ClosedOpenHalfOpenClosed(t *testing.T) {
	clock := newFakeClock()
	b := newBreakerWithClock(&Config{
		MaxFailures:       2,
		ResetTimeout:      10 * time.Second,
		HalfOpenSuccesses: 2,
	}, clock)

	// 1) Closed
	if b.GetState() != Closed {
		t.Fatalf("step 1: expected Closed, got %s", b.GetState())
	}

	// 2) Closed → Open (2 failures)
	for i := 0; i < 2; i++ {
		_ = b.Execute(context.Background(), func(ctx context.Context) error {
			return errTest
		})
	}
	if b.GetState() != Open {
		t.Fatalf("step 2: expected Open, got %s", b.GetState())
	}

	// 3) Open — calls rejected
	err := b.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})
	if !errors.Is(err, ErrOpen) {
		t.Fatalf("step 3: expected ErrOpen, got %v", err)
	}

	// 4) Open → HalfOpen (timeout elapsed, first probe succeeds)
	clock.Advance(11 * time.Second)
	err = b.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("step 4: probe error: %v", err)
	}

	// After 1 success, still HalfOpen (need 2)
	if b.GetState() != HalfOpen {
		t.Fatalf("step 4: expected HalfOpen, got %s", b.GetState())
	}

	// 5) HalfOpen → Closed (second success)
	err = b.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("step 5: probe error: %v", err)
	}
	if b.GetState() != Closed {
		t.Fatalf("step 5: expected Closed, got %s", b.GetState())
	}
}

func TestFullCycle_HalfOpenFailureReOpens_ThenRecovers(t *testing.T) {
	clock := newFakeClock()
	b := newBreakerWithClock(&Config{
		MaxFailures:       2,
		ResetTimeout:      10 * time.Second,
		HalfOpenSuccesses: 1,
	}, clock)

	// Closed → Open
	for i := 0; i < 2; i++ {
		_ = b.Execute(context.Background(), func(ctx context.Context) error {
			return errTest
		})
	}

	// Open → HalfOpen, probe fails → Open again
	clock.Advance(11 * time.Second)
	_ = b.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})
	if b.GetState() != Open {
		t.Fatalf("expected Open after failed probe, got %s", b.GetState())
	}

	// Must wait ResetTimeout again
	err := b.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})
	if !errors.Is(err, ErrOpen) {
		t.Fatalf("expected ErrOpen before second timeout, got %v", err)
	}

	// Wait again, succeed this time
	clock.Advance(11 * time.Second)
	err = b.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})
	if err != nil {
		t.Fatalf("expected success on second probe, got %v", err)
	}
	if b.GetState() != Closed {
		t.Fatalf("expected Closed after successful probe, got %s", b.GetState())
	}
}

// --- Concurrency ---

func TestConcurrent_ManyGoroutines(t *testing.T) {
	clock := newFakeClock()
	b := newBreakerWithClock(&Config{
		MaxFailures:       5,
		ResetTimeout:      1 * time.Second,
		HalfOpenSuccesses: 2,
	}, clock)

	var wg sync.WaitGroup
	var executed atomic.Int64
	var rejected atomic.Int64

	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := b.Execute(context.Background(), func(ctx context.Context) error {
				if i%3 == 0 {
					return errTest
				}
				return nil
			})
			if errors.Is(err, ErrOpen) {
				rejected.Add(1)
			} else {
				executed.Add(1)
			}
		}(i)
	}
	wg.Wait()

	total := executed.Load() + rejected.Load()
	if total != 100 {
		t.Fatalf("expected 100 total, got %d", total)
	}

	state := b.GetState()
	if state != Closed && state != Open && state != HalfOpen {
		t.Fatalf("unexpected state: %s", state)
	}
}

// --- State.String() ---

func TestStateString(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{Closed, "closed"},
		{Open, "open"},
		{HalfOpen, "half-open"},
		{State(99), "unknown(99)"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.state.String(); got != tt.want {
				t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
			}
		})
	}
}

// --- ErrOpen is returned, not wrapped ---

func TestErrOpen_IsDetectable(t *testing.T) {
	clock := newFakeClock()
	b := newBreakerWithClock(&Config{MaxFailures: 1, ResetTimeout: time.Hour}, clock)

	_ = b.Execute(context.Background(), func(ctx context.Context) error {
		return errTest
	})

	err := b.Execute(context.Background(), func(ctx context.Context) error {
		return nil
	})

	if !errors.Is(err, ErrOpen) {
		t.Fatalf("expected errors.Is(err, ErrOpen), got %v", err)
	}
}

// --- fn error is propagated unchanged ---

func TestClosedState_PropagatesWrappedErrors(t *testing.T) {
	b := newTestBreaker(&Config{MaxFailures: 5})

	inner := errors.New("inner")
	wrapped := fmt.Errorf("outer: %w", inner)

	err := b.Execute(context.Background(), func(ctx context.Context) error {
		return wrapped
	})

	if !errors.Is(err, inner) {
		t.Fatalf("expected unwrappable to inner, got %v", err)
	}
	if err != wrapped {
		t.Fatalf("expected exact wrapped error, got %v", err)
	}
}
