package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"

	er "github.com/sanchey92/order-processor/internal/domain/errors"
	"github.com/sanchey92/order-processor/internal/domain/model"
	"github.com/sanchey92/order-processor/pkg/breaker"
)

type OrderProcessor interface {
	ProcessCommand(ctx context.Context, cmd *model.CreateOrderCommand, orderID string) error
}

type KafkaHandler struct {
	orderProcessor OrderProcessor
	logger         *slog.Logger
}

func NewKafkaHandler(op OrderProcessor, l *slog.Logger) *KafkaHandler {
	return &KafkaHandler{
		orderProcessor: op,
		logger:         l,
	}
}

func (h *KafkaHandler) Handle(ctx context.Context, msg *kafka.Message) error {
	var cmd model.CreateOrderCommand
	if err := json.Unmarshal(msg.Value, &cmd); err != nil {
		return &er.NonRetriableError{Cause: fmt.Errorf("invalid payload: %w", err)}
	}
	orderID := headerValue(msg.Headers, "order-id")
	if orderID == "" {
		return &er.NonRetriableError{Cause: fmt.Errorf("missing order-id header")}
	}

	if err := h.orderProcessor.ProcessCommand(ctx, &cmd, orderID); err != nil {
		// Circuit breaker open -> retriable, will come back
		if errors.Is(err, breaker.ErrOpen) {
			return &er.RetriableError{Cause: err}
		}

		// Saga failed but compensated → system is consistent.
		h.logger.Error("saga failed (compensated)", slog.String("order_id", orderID), slog.Any("error", err))
		return nil // Commit offset — retrying won't help.
	}
	return nil
}

func headerValue(headers []kafka.Header, key string) string {
	for _, h := range headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}
