package saga

import (
	"context"
	"fmt"
	"log/slog"
)

type Step struct {
	Name       string
	Execute    func(ctx context.Context) error
	Compensate func(ctx context.Context) error
}

type CompensationError struct {
	Step  string
	Error error
}

type Result struct {
	Success            bool
	FailedStep         string
	Error              error
	CompensationErrors []CompensationError
}

// IsPoisoned returns true if compensation also failed → manual intervention needed.
func (r *Result) IsPoisoned() bool {
	return !r.Success && len(r.CompensationErrors) > 0
}

type Orchestrator struct {
	name  string
	steps []Step
	log   *slog.Logger
}

func New(name string, steps []Step, log *slog.Logger) *Orchestrator {
	return &Orchestrator{
		name:  name,
		steps: steps,
		log:   log,
	}
}

func (o *Orchestrator) Execute(ctx context.Context) *Result {
	o.log.Info("saga started", slog.String("saga", o.name), slog.Int("steps", len(o.steps)))

	var completed []Step
	for i, step := range o.steps {
		if err := ctx.Err(); err != nil {
			return o.fail(ctx, step.Name, err, completed)
		}

		o.log.Info("step executing",
			slog.String("saga", o.name),
			slog.String("step", step.Name),
			slog.Int("n", i+1),
		)

		if err := o.safeExecute(ctx, step); err != nil {
			return o.fail(ctx, step.Name, err, completed)
		}

		completed = append(completed, step)
	}

	o.log.Info("saga completed", slog.String("saga", o.name))

	return &Result{Success: true}
}

func (o *Orchestrator) fail(ctx context.Context, stepName string, err error, completed []Step) *Result {
	o.log.Error("step failed",
		slog.String("saga", o.name),
		slog.String("step", stepName),
		slog.Any("error", err),
	)

	compErrors := o.compensate(context.WithoutCancel(ctx), completed)

	return &Result{
		FailedStep:         stepName,
		Error:              err,
		CompensationErrors: compErrors,
	}
}

func (o *Orchestrator) compensate(ctx context.Context, completed []Step) []CompensationError {
	var errs []CompensationError
	for i := len(completed) - 1; i >= 0; i-- {
		s := completed[i]
		if s.Compensate == nil {
			continue
		}

		o.log.Info("compensating",
			slog.String("saga", o.name),
			slog.String("step", s.Name),
		)

		if err := o.safeCompensate(ctx, s); err != nil {
			o.log.Error("compensation failed",
				slog.String("saga", o.name),
				slog.String("step", s.Name),
				slog.Any("error", err),
			)
			errs = append(errs, CompensationError{Step: s.Name, Error: err})
		}
	}
	return errs
}

func (o *Orchestrator) safeExecute(ctx context.Context, step Step) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in step %s: %v", step.Name, r)
		}
	}()
	return step.Execute(ctx)
}

func (o *Orchestrator) safeCompensate(ctx context.Context, step Step) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic in compensate %s: %v", step.Name, r)
		}
	}()
	return step.Compensate(ctx)
}
