package pg

import (
	"context"
	"fmt"
)

func (s *Storage) TryInsertProcessedEvent(ctx context.Context, key string) (bool, error) {
	query := `INSERT INTO processed_events (idempotency_key, processed_at) 
              VALUES ($1, now())
              ON CONFLICT (idempotency_key) DO NOTHING`

	tag, err := s.conn(ctx).Exec(ctx, query, key)
	if err != nil {
		return false, fmt.Errorf("try insert processed event: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}
