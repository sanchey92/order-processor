package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"

	er "github.com/sanchey92/order-processor/internal/domain/errors"
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
	pub Publisher,
	dlqTopic string,
	maxRetries int,
	log *slog.Logger) k.Handler {

	h := handler

	h = withIdempotency(txr, checker, log, h)

	h = withPoisonPill(pub, dlqTopic, maxRetries, log, h)

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

func withPoisonPill(pub Publisher, dlqTopic string, maxRetries int, log *slog.Logger, next k.Handler) k.Handler {
	return func(ctx context.Context, msg *kafka.Message) error {
		err := next(ctx, msg)
		if err == nil {
			return nil
		}

		retries, e := getRetryCount(msg)
		if e != nil {
			return fmt.Errorf("get retry count: %w", e)
		}

		var nr *er.NonRetriableError
		if errors.As(err, &nr) {
			log.Error("non-retriable error, sending to DLQ",
				slog.Int64("offset", int64(msg.TopicPartition.Offset)),
				slog.Any("error", err))
			return sendToDLQ(ctx, pub, dlqTopic, msg, err)
		}

		if retries >= maxRetries {
			log.Warn("retries exhausted, sending to DLQ",
				slog.Int("retries", retries),
				slog.Int("max", maxRetries),
				slog.Any("error", err))
			return sendToDLQ(ctx, pub, dlqTopic, msg, err)
		}

		log.Warn("retriable error, re-publishing",
			slog.Int("retry", retries+1),
			slog.Int("max", maxRetries),
			slog.Any("error", err))
		return rePublish(ctx, pub, msg, retries+1)
	}
}

func getRetryCount(msg *kafka.Message) (int, error) {
	for _, h := range msg.Headers {
		if h.Key == "x-retry-count" {
			var c int
			if _, err := fmt.Sscanf(string(h.Value), "%d", &c); err != nil {
				return 0, fmt.Errorf("fmt.Sscanf: %w", err)
			}
			return c, nil
		}
	}
	return 0, nil
}

func rePublish(ctx context.Context, pub Publisher, msg *kafka.Message, retryCount int) error {
	headers := make([]kafka.Header, 0, len(msg.Headers)+1)
	for _, h := range msg.Headers {
		if h.Key != "x-retry-count" {
			headers = append(headers, h)
		}
	}
	headers = append(headers, kafka.Header{
		Key:   "x-retry-count",
		Value: []byte(fmt.Sprintf("%d", retryCount)),
	})
	if err := pub.Publish(ctx, *msg.TopicPartition.Topic, msg.Key, msg.Value, headers); err != nil {
		return fmt.Errorf("re-publish: %w", err)
	}
	return nil
}

func sendToDLQ(ctx context.Context, pub Publisher, dlqTopic string, msg *kafka.Message, handlerErr error) error {
	headers := make([]kafka.Header, len(msg.Headers))
	copy(headers, msg.Headers)
	headers = append(headers,
		kafka.Header{Key: "x-original-topic", Value: []byte(*msg.TopicPartition.Topic)},
		kafka.Header{Key: "x-original-partition", Value: []byte(fmt.Sprintf("%d", msg.TopicPartition.Partition))},
		kafka.Header{Key: "x-original-offset", Value: []byte(fmt.Sprintf("%d", msg.TopicPartition.Offset))},
		kafka.Header{Key: "x-error", Value: []byte(handlerErr.Error())},
		kafka.Header{Key: "x-failed-at", Value: []byte(time.Now().UTC().Format(time.RFC3339))},
	)
	if err := pub.Publish(ctx, dlqTopic, msg.Key, msg.Value, headers); err != nil {
		return fmt.Errorf("DLQ publish: %w", err)
	}

	return nil
}
