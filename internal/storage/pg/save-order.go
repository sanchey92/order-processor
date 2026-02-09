package pg

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sanchey92/order-processor/internal/domain/model"
)

func (s *Storage) SaveTx(ctx context.Context, o *model.Order, msg *model.OutboxMessage) error {
	return s.RunInTx(ctx, func(ctx context.Context) error {
		if err := s.InsertOrder(ctx, o); err != nil {
			return fmt.Errorf("insert order: %w", err)
		}
		if err := s.InsertOutboxMsg(ctx, msg); err != nil {
			return fmt.Errorf("insert outbox msg: %w", err)
		}
		return nil
	})
}

func (s *Storage) InsertOrder(ctx context.Context, o *model.Order) error {
	query := `INSERT INTO orders (id, user_id, items, amount, status, created_at, updated_at)
              VALUES ($1, $2, $3, $4, $5, $6, $7)`

	if _, err := s.conn(ctx).Exec(ctx, query, o.ID, o.UserID, o.Items, o.Amount, o.Status, o.CreatedAt, o.UpdatedAt); err != nil {
		return fmt.Errorf("insert order: %w", err)
	}
	return nil
}

func (s *Storage) InsertOutboxMsg(ctx context.Context, msg *model.OutboxMessage) error {
	headers, err := json.Marshal(msg.Headers)
	if err != nil {
		return fmt.Errorf("json marshal: %w", err)
	}
	query := `INSERT INTO outbox (topic, key, event_type, payload, headers)
              VALUES ($1, $2, $3, $4, $5)`

	if _, err = s.conn(ctx).Exec(ctx, query, msg.Topic, msg.Key, msg.EventType, msg.Payload, headers); err != nil {
		return fmt.Errorf("insert outbox msg: %w", err)
	}
	return nil
}
