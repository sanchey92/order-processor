package pg

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	er "github.com/sanchey92/order-processor/internal/domain/errors"
	"github.com/sanchey92/order-processor/internal/domain/model"
)

func (s *Storage) FindByID(ctx context.Context, id string) (*model.Order, error) {
	query := `SELECT id, user_id, items, amount, status, created_at, updated_at
              FROM orders WHERE id = $1`

	var o model.Order
	var itemsJSON []byte

	err := s.pool.QueryRow(ctx, query, id).
		Scan(&o.ID, &o.UserID, &itemsJSON, &o.Amount, &o.Status, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, er.ErrOrderNotFound
		}
		return nil, fmt.Errorf("query: %w", err)
	}
	if err = json.Unmarshal(itemsJSON, &o.Items); err != nil {
		return nil, fmt.Errorf("unmarshal items: %w", err)
	}
	return &o, nil
}
