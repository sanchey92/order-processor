package pg

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/sanchey92/order-processor/internal/domain/model"
)

func (s *Storage) SaveTx(ctx context.Context, o *model.Order, msg *model.OutboxMessage) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("SaveTx: %w", err)
	}
	defer func() {
		if err = tx.Rollback(ctx); err != nil {
			s.logger.Info("failed to rollback transaction")
		}
	}()

	if err = s.InsertOrderTx(ctx, tx, o); err != nil {
		return fmt.Errorf("insert order: %w", err)
	}

	if err = s.InsertOutboxMsgTx(ctx, tx, msg); err != nil {
		return fmt.Errorf("insert outbox msg: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("tx commit: %w", err)
	}

	return nil
}

func (s *Storage) InsertOrderTx(ctx context.Context, tx pgx.Tx, o *model.Order) error {
	query := `INSERT INTO orders (id, user_id, items, amount, status, created_at, updated_at)
              VALUES ($1, $2, $3, $4, $5, $6, $7)`

	_, err := tx.Exec(ctx, query, o.ID, o.UserID, o.Items, o.Amount, o.Status, o.CreatedAt, o.UpdatedAt)
	if err != nil {
		return fmt.Errorf("tx exec: %w", err)
	}
	return nil
}

func (s *Storage) InsertOutboxMsgTx(ctx context.Context, tx pgx.Tx, msg *model.OutboxMessage) error {
	headers, err := json.Marshal(msg.Headers)
	if err != nil {
		return fmt.Errorf("json marshal: %w", err)
	}
	query := `INSERT INTO outbox (topic, key, event_type, payload, headers)
	          VALUES ($1, $2, $3, $4, $5)`

	_, err = tx.Exec(ctx, query, msg.Topic, msg.Key, msg.EventType, msg.Payload, headers)
	if err != nil {
		return fmt.Errorf("tx exec: %w", err)
	}
	return nil
}
