package order

import (
	"context"
	"log/slog"

	"github.com/sanchey92/order-processor/internal/domain/model"
)

type Saver interface {
	SaveTx(ctx context.Context, o *model.Order, msg *model.OutboxMessage) error
}

type Service struct {
	logger *slog.Logger
	saver  Saver
}

func NewOrderService(l *slog.Logger, saver Saver) *Service {
	return &Service{
		logger: l,
		saver:  saver,
	}
}
