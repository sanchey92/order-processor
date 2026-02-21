package order

import (
	"context"
	"fmt"

	"github.com/sanchey92/order-processor/internal/domain/model"
)

func (s *Service) GetOrder(ctx context.Context, id string) (*model.Order, error) {
	o, err := s.getter.FindByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("service: get order: %w", err)
	}
	return o, nil
}
