package pg

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/sanchey92/order-processor/internal/domain/model"
)

func (s *Storage) GetBatch(ctx context.Context, batchSize int) ([]*model.OutboxMessage, error) {
	query := `SELECT id, topic, key, event_type, payload, headers
              FROM outbox
              WHERE published_at IS NULL AND retry_count < 5
              ORDER BY created_at
              LIMIT $1
              FOR UPDATE SKIP LOCKED`

	rows, err := s.conn(ctx).Query(ctx, query, batchSize)
	if err != nil {
		return nil, fmt.Errorf("get batch: %w", err)
	}
	defer rows.Close()

	var msgs []*model.OutboxMessage
	for rows.Next() {
		msg := &model.OutboxMessage{}
		var headersJSON []byte
		if err = rows.Scan(&msg.ID, &msg.Topic, &msg.Key, &msg.EventType, &msg.Payload, &headersJSON); err != nil {
			return nil, fmt.Errorf("scan outbox message: %w", err)
		}
		var headers map[string]string
		if err = json.Unmarshal(headersJSON, &headers); err != nil {
			return nil, fmt.Errorf("unmarshal headers: %w", err)
		}
		msg.Headers = headers
		msgs = append(msgs, msg)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate outbox rows: %w", err)
	}

	return msgs, nil
}

func (s *Storage) UpdateRetryCount(ctx context.Context, id int64, errMsg string) error {
	query := `UPDATE outbox
              SET
                  retry_count = retry_count + 1,
                  last_error = $2
              WHERE id = $1`

	if _, err := s.conn(ctx).Exec(ctx, query, id, errMsg); err != nil {
		return fmt.Errorf("update retry count: %w", err)
	}
	return nil
}

func (s *Storage) MarkPublished(ctx context.Context, id int64) error {
	query := `UPDATE outbox
              SET published_at = now()
              WHERE id = $1`

	if _, err := s.conn(ctx).Exec(ctx, query, id); err != nil {
		return fmt.Errorf("mark published: %w", err)
	}
	return nil
}
