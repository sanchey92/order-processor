package handler

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"

	k "github.com/sanchey92/order-processor/pkg/kafka"
)

type Publisher interface {
	Publish(ctx context.Context, topic string, key, val []byte, headers []kafka.Header) error
}

type TxRunner interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context) error) error
}

type IdempotencyChecker interface {
	TryInsertProcessedEvent(ctx context.Context, key string) (bool, error)
}

func BuildHandler(
	handler k.Handler,
	txr TxRunner,
	checker IdempotencyChecker,
	publisher Publisher,
	log *slog.Logger) k.Handler {
	h := handler

	h = withIdempotency(txr, checker, log, h)

	return h
}

func withIdempotency(txr TxRunner, checker IdempotencyChecker, log *slog.Logger, next k.Handler) k.Handler {
	return func(ctx context.Context, msg *kafka.Message) error {
		key := fmt.Sprintf("%s:%d:%d",
			*msg.TopicPartition.Topic,
			msg.TopicPartition.Partition,
			msg.TopicPartition.Offset)

		return txr.RunInTx(ctx, func(txCtx context.Context) error {
			isNew, err := checker.TryInsertProcessedEvent(txCtx, key)
			if err != nil {
				return fmt.Errorf("idempotency check: %w", err)
			}
			if !isNew {
				log.Debug("duplicate skipped", slog.String("key", key))
				return nil
			}
			return next(txCtx, msg)
		})
	}
}
