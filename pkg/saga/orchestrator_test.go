package saga

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

var (
	errStep = errors.New("step error")
	errComp = errors.New("compensation error")
	nopLog  = slog.New(slog.NewTextHandler(&strings.Builder{}, nil))
)

func okStep(name string, trace *[]string) Step {
	return Step{
		Name: name,
		Execute: func(_ context.Context) error {
			*trace = append(*trace, "exec:"+name)
			return nil
		},
		Compensate: func(_ context.Context) error {
			*trace = append(*trace, "comp:"+name)
			return nil
		},
	}
}

func failStep(name string, trace *[]string, err error) Step {
	return Step{
		Name: name,
		Execute: func(_ context.Context) error {
			*trace = append(*trace, "exec:"+name)
			return err
		},
		Compensate: func(_ context.Context) error {
			*trace = append(*trace, "comp:"+name)
			return nil
		},
	}
}

// --- Happy path ---

func TestExecute_AllStepsSucceed(t *testing.T) {
	var trace []string
	o := New("test", []Step{
		okStep("A", &trace),
		okStep("B", &trace),
		okStep("C", &trace),
	}, nopLog)

	r := o.Execute(context.Background())
	if !r.Success {
		t.Fatalf("expected success, got failure: %v", r.Error)
	}
	if r.FailedStep != "" {
		t.Fatalf("expected empty FailedStep, got %q", r.FailedStep)
	}
	if r.Error != nil {
		t.Fatalf("expected nil Error, got %v", r.Error)
	}
	if len(r.CompensationErrors) != 0 {
		t.Fatalf("expected no compensation errors, got %d", len(r.CompensationErrors))
	}

	want := "exec:A,exec:B,exec:C"
	got := strings.Join(trace, ",")
	if got != want {
		t.Fatalf("trace: got %q, want %q", got, want)
	}
}

func TestExecute_EmptySteps(t *testing.T) {
	o := New("empty", nil, nopLog)

	r := o.Execute(context.Background())
	if !r.Success {
		t.Fatal("expected success for empty saga")
	}
}

func TestExecute_SingleStepSuccess(t *testing.T) {
	var trace []string
	o := New("single", []Step{okStep("only", &trace)}, nopLog)

	r := o.Execute(context.Background())
	if !r.Success {
		t.Fatalf("expected success, got %v", r.Error)
	}

	want := "exec:only"
	got := strings.Join(trace, ",")
	if got != want {
		t.Fatalf("trace: got %q, want %q", got, want)
	}
}

// --- Compensation order ---

func TestExecute_FailureCompensatesInReverseOrder(t *testing.T) {
	var trace []string
	o := New("test", []Step{
		okStep("A", &trace),
		okStep("B", &trace),
		failStep("C", &trace, errStep),
	}, nopLog)

	r := o.Execute(context.Background())
	if r.Success {
		t.Fatal("expected failure")
	}
	if r.FailedStep != "C" {
		t.Fatalf("expected FailedStep=C, got %q", r.FailedStep)
	}
	if !errors.Is(r.Error, errStep) {
		t.Fatalf("expected errStep, got %v", r.Error)
	}

	want := "exec:A,exec:B,exec:C,comp:B,comp:A"
	got := strings.Join(trace, ",")
	if got != want {
		t.Fatalf("trace: got %q, want %q", got, want)
	}
}

func TestExecute_FirstStepFails_NoCompensation(t *testing.T) {
	var trace []string
	o := New("test", []Step{
		failStep("A", &trace, errStep),
		okStep("B", &trace),
	}, nopLog)

	r := o.Execute(context.Background())
	if r.Success {
		t.Fatal("expected failure")
	}
	if r.FailedStep != "A" {
		t.Fatalf("expected FailedStep=A, got %q", r.FailedStep)
	}

	want := "exec:A"
	got := strings.Join(trace, ",")
	if got != want {
		t.Fatalf("trace: got %q, want %q", got, want)
	}
}

func TestExecute_MiddleStepFails_CompensatesOnlyCompleted(t *testing.T) {
	var trace []string
	o := New("test", []Step{
		okStep("A", &trace),
		failStep("B", &trace, errStep),
		okStep("C", &trace),
	}, nopLog)

	r := o.Execute(context.Background())
	if r.FailedStep != "B" {
		t.Fatalf("expected FailedStep=B, got %q", r.FailedStep)
	}

	want := "exec:A,exec:B,comp:A"
	got := strings.Join(trace, ",")
	if got != want {
		t.Fatalf("trace: got %q, want %q", got, want)
	}
}

// --- Nil Compensate ---

func TestExecute_NilCompensateSkipped(t *testing.T) {
	var trace []string
	steps := []Step{
		{
			Name: "no-comp",
			Execute: func(_ context.Context) error {
				trace = append(trace, "exec:no-comp")
				return nil
			},
			Compensate: nil,
		},
		okStep("B", &trace),
		failStep("C", &trace, errStep),
	}
	o := New("test", steps, nopLog)

	r := o.Execute(context.Background())
	if r.Success {
		t.Fatal("expected failure")
	}

	want := "exec:no-comp,exec:B,exec:C,comp:B"
	got := strings.Join(trace, ",")
	if got != want {
		t.Fatalf("trace: got %q, want %q", got, want)
	}
}

// --- Compensation errors ---

func TestExecute_CompensationError_Collected(t *testing.T) {
	var trace []string
	steps := []Step{
		{
			Name: "A",
			Execute: func(_ context.Context) error {
				trace = append(trace, "exec:A")
				return nil
			},
			Compensate: func(_ context.Context) error {
				trace = append(trace, "comp:A")
				return errComp
			},
		},
		failStep("B", &trace, errStep),
	}
	o := New("test", steps, nopLog)

	r := o.Execute(context.Background())
	if r.Success {
		t.Fatal("expected failure")
	}
	if len(r.CompensationErrors) != 1 {
		t.Fatalf("expected 1 compensation error, got %d", len(r.CompensationErrors))
	}
	if r.CompensationErrors[0].Step != "A" {
		t.Fatalf("expected comp error step=A, got %q", r.CompensationErrors[0].Step)
	}
	if !errors.Is(r.CompensationErrors[0].Error, errComp) {
		t.Fatalf("expected errComp, got %v", r.CompensationErrors[0].Error)
	}
}

func TestExecute_MultipleCompensationErrors(t *testing.T) {
	failComp := func(name string, trace *[]string) Step {
		return Step{
			Name: name,
			Execute: func(_ context.Context) error {
				*trace = append(*trace, "exec:"+name)
				return nil
			},
			Compensate: func(_ context.Context) error {
				*trace = append(*trace, "comp:"+name)
				return errComp
			},
		}
	}

	var trace []string
	o := New("test", []Step{
		failComp("A", &trace),
		failComp("B", &trace),
		failStep("C", &trace, errStep),
	}, nopLog)

	r := o.Execute(context.Background())
	if len(r.CompensationErrors) != 2 {
		t.Fatalf("expected 2 compensation errors, got %d", len(r.CompensationErrors))
	}
	if r.CompensationErrors[0].Step != "B" {
		t.Fatalf("expected first comp error step=B (reverse), got %q", r.CompensationErrors[0].Step)
	}
	if r.CompensationErrors[1].Step != "A" {
		t.Fatalf("expected second comp error step=A (reverse), got %q", r.CompensationErrors[1].Step)
	}
}

func TestExecute_CompensationContinuesAfterError(t *testing.T) {
	var trace []string
	steps := []Step{
		okStep("A", &trace),
		{
			Name: "B",
			Execute: func(_ context.Context) error {
				trace = append(trace, "exec:B")
				return nil
			},
			Compensate: func(_ context.Context) error {
				trace = append(trace, "comp:B")
				return errComp
			},
		},
		failStep("C", &trace, errStep),
	}
	o := New("test", steps, nopLog)

	r := o.Execute(context.Background())

	// B compensation fails, but A compensation should still run
	want := "exec:A,exec:B,exec:C,comp:B,comp:A"
	got := strings.Join(trace, ",")
	if got != want {
		t.Fatalf("trace: got %q, want %q", got, want)
	}
	if len(r.CompensationErrors) != 1 {
		t.Fatalf("expected 1 compensation error, got %d", len(r.CompensationErrors))
	}
}

// --- IsPoisoned ---

func TestIsPoisoned_TrueWhenCompensationFailed(t *testing.T) {
	r := &Result{
		Success:            false,
		CompensationErrors: []CompensationError{{Step: "A", Error: errComp}},
	}
	if !r.IsPoisoned() {
		t.Fatal("expected IsPoisoned=true")
	}
}

func TestIsPoisoned_FalseWhenCompensationSucceeded(t *testing.T) {
	r := &Result{
		Success: false,
		Error:   errStep,
	}
	if r.IsPoisoned() {
		t.Fatal("expected IsPoisoned=false when no compensation errors")
	}
}

func TestIsPoisoned_FalseOnSuccess(t *testing.T) {
	r := &Result{Success: true}
	if r.IsPoisoned() {
		t.Fatal("expected IsPoisoned=false on success")
	}
}

// --- Panic handling ---

func TestExecute_PanicInStep_RecoveredAsError(t *testing.T) {
	var trace []string
	steps := []Step{
		okStep("A", &trace),
		{
			Name: "panic-step",
			Execute: func(_ context.Context) error {
				panic("boom")
			},
			Compensate: func(_ context.Context) error {
				trace = append(trace, "comp:panic-step")
				return nil
			},
		},
		okStep("C", &trace),
	}
	o := New("test", steps, nopLog)

	r := o.Execute(context.Background())
	if r.Success {
		t.Fatal("expected failure on panic")
	}
	if r.FailedStep != "panic-step" {
		t.Fatalf("expected FailedStep=panic-step, got %q", r.FailedStep)
	}
	if !strings.Contains(r.Error.Error(), "panic in step panic-step: boom") {
		t.Fatalf("expected panic error message, got %v", r.Error)
	}

	// Only A was completed, so only A should be compensated
	want := "exec:A,comp:A"
	got := strings.Join(trace, ",")
	if got != want {
		t.Fatalf("trace: got %q, want %q", got, want)
	}
}

func TestExecute_PanicInCompensate_RecoveredAsCompensationError(t *testing.T) {
	var trace []string
	steps := []Step{
		{
			Name: "A",
			Execute: func(_ context.Context) error {
				trace = append(trace, "exec:A")
				return nil
			},
			Compensate: func(_ context.Context) error {
				panic("comp-boom")
			},
		},
		failStep("B", &trace, errStep),
	}
	o := New("test", steps, nopLog)

	r := o.Execute(context.Background())
	if r.Success {
		t.Fatal("expected failure")
	}
	if len(r.CompensationErrors) != 1 {
		t.Fatalf("expected 1 compensation error, got %d", len(r.CompensationErrors))
	}
	if !strings.Contains(r.CompensationErrors[0].Error.Error(), "panic in compensate A: comp-boom") {
		t.Fatalf("expected panic comp error, got %v", r.CompensationErrors[0].Error)
	}
}

func TestExecute_PanicInStep_DoesNotPropagateOutward(t *testing.T) {
	steps := []Step{
		{
			Name:    "panic",
			Execute: func(_ context.Context) error { panic("should not escape") },
		},
	}
	o := New("test", steps, nopLog)

	// Should not panic — recovered internally
	r := o.Execute(context.Background())
	if r.Success {
		t.Fatal("expected failure")
	}
}

// --- Context cancellation ---

func TestExecute_CancelledContextBeforeFirstStep(t *testing.T) {
	var trace []string
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	o := New("test", []Step{
		okStep("A", &trace),
		okStep("B", &trace),
	}, nopLog)

	r := o.Execute(ctx)
	if r.Success {
		t.Fatal("expected failure on cancelled context")
	}
	if !errors.Is(r.Error, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", r.Error)
	}
	if r.FailedStep != "A" {
		t.Fatalf("expected FailedStep=A, got %q", r.FailedStep)
	}

	// No steps executed, no compensation
	if len(trace) != 0 {
		t.Fatalf("expected empty trace, got %q", strings.Join(trace, ","))
	}
}

func TestExecute_CancelledContextBetweenSteps(t *testing.T) {
	var trace []string
	ctx, cancel := context.WithCancel(context.Background())

	steps := []Step{
		{
			Name: "A",
			Execute: func(_ context.Context) error {
				trace = append(trace, "exec:A")
				cancel() // cancel after A succeeds
				return nil
			},
			Compensate: func(_ context.Context) error {
				trace = append(trace, "comp:A")
				return nil
			},
		},
		okStep("B", &trace),
	}
	o := New("test", steps, nopLog)

	r := o.Execute(ctx)
	if r.Success {
		t.Fatal("expected failure")
	}
	if r.FailedStep != "B" {
		t.Fatalf("expected FailedStep=B (detected before B), got %q", r.FailedStep)
	}

	want := "exec:A,comp:A"
	got := strings.Join(trace, ",")
	if got != want {
		t.Fatalf("trace: got %q, want %q", got, want)
	}
}

func TestExecute_WithoutCancel_CompensationRunsWithDeadContext(t *testing.T) {
	var trace []string
	var compCtxErr error

	ctx, cancel := context.WithCancel(context.Background())
	steps := []Step{
		{
			Name: "A",
			Execute: func(_ context.Context) error {
				trace = append(trace, "exec:A")
				return nil
			},
			Compensate: func(ctx context.Context) error {
				compCtxErr = ctx.Err()
				trace = append(trace, "comp:A")
				return nil
			},
		},
		{
			Name: "B",
			Execute: func(_ context.Context) error {
				cancel() // cancel context
				return errStep
			},
			Compensate: nil,
		},
	}
	o := New("test", steps, nopLog)

	r := o.Execute(ctx)
	if r.Success {
		t.Fatal("expected failure")
	}

	// Compensation should have run with a non-cancelled context (WithoutCancel)
	if compCtxErr != nil {
		t.Fatalf("compensation ctx should not be cancelled, got %v", compCtxErr)
	}

	want := "exec:A,comp:A"
	got := strings.Join(trace, ",")
	if got != want {
		t.Fatalf("trace: got %q, want %q", got, want)
	}
}

// --- Result fields ---

func TestResult_FieldsPopulatedCorrectly(t *testing.T) {
	var trace []string
	o := New("order-saga", []Step{
		okStep("reserve", &trace),
		okStep("charge", &trace),
		failStep("ship", &trace, errStep),
	}, nopLog)

	r := o.Execute(context.Background())

	if r.Success {
		t.Fatal("expected failure")
	}
	if r.FailedStep != "ship" {
		t.Fatalf("FailedStep: got %q, want %q", r.FailedStep, "ship")
	}
	if !errors.Is(r.Error, errStep) {
		t.Fatalf("Error: got %v, want errStep", r.Error)
	}
	if len(r.CompensationErrors) != 0 {
		t.Fatalf("expected 0 compensation errors, got %d", len(r.CompensationErrors))
	}
	if r.IsPoisoned() {
		t.Fatal("should not be poisoned when compensation succeeds")
	}
}

func TestResult_SuccessFieldsAreZeroValues(t *testing.T) {
	var trace []string
	o := New("ok-saga", []Step{okStep("A", &trace)}, nopLog)

	r := o.Execute(context.Background())

	if !r.Success {
		t.Fatal("expected success")
	}
	if r.FailedStep != "" {
		t.Fatalf("expected empty FailedStep, got %q", r.FailedStep)
	}
	if r.Error != nil {
		t.Fatalf("expected nil Error, got %v", r.Error)
	}
	if r.CompensationErrors != nil {
		t.Fatalf("expected nil CompensationErrors, got %v", r.CompensationErrors)
	}
}
