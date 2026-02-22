package pg

import (
	"context"
	"fmt"

	"github.com/sanchey92/order-processor/internal/domain/model"
)

func (s *Storage) UpdateStatusTx(ctx context.Context, orderID string, status model.OrderStatus, msg *model.OutboxMessage) error {
	return s.RunInTx(ctx, func(ctx context.Context) error {
		query := `UPDATE orders
                  SET status = $2, updated_at = now()
                  WHERE id = $1`
		// conn(ctx) вернет tx из контекста
		if _, err := s.conn(ctx).Exec(ctx, query, orderID, status); err != nil {
			return fmt.Errorf("update order status: %w", err)
		}
		if err := s.InsertOutboxMsg(ctx, msg); err != nil {
			return fmt.Errorf("insert outbox message: %w", err)
		}
		return nil
	})
}
