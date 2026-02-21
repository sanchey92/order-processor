package order

import (
	"context"
	"log/slog"

	"github.com/sanchey92/order-processor/internal/domain/model"
)

type Saver interface {
	SaveTx(ctx context.Context, o *model.Order, msg *model.OutboxMessage) error
}

type Getter interface {
	FindByID(ctx context.Context, id string) (*model.Order, error)
}

type Service struct {
	logger *slog.Logger
	saver  Saver
	getter Getter
}

func NewOrderService(l *slog.Logger, saver Saver, getter Getter) *Service {
	return &Service{
		logger: l,
		saver:  saver,
		getter: getter,
	}
}
