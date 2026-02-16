package order

import (
	"context"

	"github.com/sanchey92/order-processor/internal/domain/model"
)

func (s *Service) ProcessCommand(ctx context.Context, cmd *model.CreateOrderCommand, orderId string) error {
	// TODO: implement with saga
	return nil
}
