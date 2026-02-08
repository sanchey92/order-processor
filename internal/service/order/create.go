package order

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/sanchey92/order-processor/internal/domain/model"
)

func (s *Service) Create(ctx context.Context, cmd *model.CreateOrderCommand) (*model.Order, error) {
	order := model.NewOrder(cmd.UserID, cmd.Items)

	payload, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("service.Create(): %w", err)
	}

	outboxMsg := &model.OutboxMessage{
		Topic:     "order-commands",
		Key:       order.UserID,
		EventType: "CreateOrder",
		Payload:   payload,
		Headers:   map[string]string{"order-id": order.ID},
	}

	if err = s.saver.SaveTx(ctx, order, outboxMsg); err != nil {
		return nil, fmt.Errorf("service.Create: %w", err)
	}

	s.logger.Info("order created", slog.String("id", order.ID), slog.Int64("amount", order.Amount))
	return order, nil
}
