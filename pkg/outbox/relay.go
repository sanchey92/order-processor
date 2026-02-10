package outbox

import (
	"context"
	"log/slog"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"

	"github.com/sanchey92/order-processor/internal/domain/model"
)

type RelayRepo interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context) error) error
	GetBatch(ctx context.Context, batchSize int) ([]*model.OutboxMessage, error)
	UpdateRetryCount(ctx context.Context, id int64, errMsg string) error
	MarkPublished(ctx context.Context, id int64) error
}

type Publisher interface {
	Publish(ctx context.Context, topic string, key, val []byte, headers []kafka.Header) error
}

type Relay struct {
	repo         RelayRepo
	publisher    Publisher
	logger       *slog.Logger
	batchSize    int
	pollInterval time.Duration
}

func NewRelay(r RelayRepo, p Publisher, l *slog.Logger, batchSize int, pollInterval time.Duration) *Relay {
	return &Relay{
		repo:         r,
		publisher:    p,
		logger:       l,
		batchSize:    batchSize,
		pollInterval: pollInterval,
	}
}

func (r *Relay) Run(ctx context.Context) error {
	r.logger.Info("outbox relay started",
		slog.Int("batch_size", r.batchSize),
		slog.Duration("poll_interval", r.pollInterval))

	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			r.logger.Info("outbox relay stopping")
			return nil
		case <-ticker.C:
			r.drain(ctx)
		}
	}
}

func (r *Relay) drain(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		processed, err := r.processBatch(ctx)
		if err != nil {
			r.logger.Error("outbox batch failed", slog.Any("error", err))
			return
		}
		if processed < r.batchSize {
			return
		}
	}
}

func (r *Relay) processBatch(ctx context.Context) (int, error) {
	var msgs []*model.OutboxMessage

	err := r.repo.RunInTx(ctx, func(ctx context.Context) error {
		var err error
		msgs, err = r.repo.GetBatch(ctx, r.batchSize)
		if err != nil {
			return err
		}

		for _, msg := range msgs {
			headers := r.buildHeaders(msg)

			if pubErr := r.publisher.Publish(ctx, msg.Topic, []byte(msg.Key), msg.Payload, headers); pubErr != nil {
				r.logger.Error("publish failed",
					slog.Int64("id", msg.ID),
					slog.Any("error", pubErr))
				if retryErr := r.repo.UpdateRetryCount(ctx, msg.ID, pubErr.Error()); retryErr != nil {
					return retryErr
				}
				continue
			}
			if err = r.repo.MarkPublished(ctx, msg.ID); err != nil {
				return err
			}
		}
		return nil
	})

	return len(msgs), err
}

func (r *Relay) buildHeaders(msg *model.OutboxMessage) []kafka.Header {
	headers := make([]kafka.Header, 0, len(msg.Headers)+1)
	for k, v := range msg.Headers {
		headers = append(headers, kafka.Header{Key: k, Value: []byte(v)})
	}
	headers = append(headers, kafka.Header{Key: "event-type", Value: []byte(msg.EventType)})
	return headers
}
