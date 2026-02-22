package order

import (
	"context"
	"log/slog"

	"github.com/sanchey92/order-processor/internal/domain/model"
	"github.com/sanchey92/order-processor/internal/http/client/payment"
	"github.com/sanchey92/order-processor/internal/http/client/warehouse"
)

type Saver interface {
	SaveTx(ctx context.Context, o *model.Order, msg *model.OutboxMessage) error
}

type Getter interface {
	FindByID(ctx context.Context, id string) (*model.Order, error)
}

type Service struct {
	paymentClient   *payment.Client
	warehouseClient *warehouse.Client
	logger          *slog.Logger
	saver           Saver
	getter          Getter
}

func NewOrderService(l *slog.Logger, saver Saver, getter Getter, wc *warehouse.Client, pc *payment.Client) *Service {
	return &Service{
		warehouseClient: wc,
		paymentClient:   pc,
		logger:          l,
		saver:           saver,
		getter:          getter,
	}
}
