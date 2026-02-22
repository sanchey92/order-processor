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

type StatusUpdater interface {
	UpdateStatusTx(ctx context.Context, orderID string, status model.OrderStatus, msg *model.OutboxMessage) error
}

type PaymentClient interface {
	Reserve(ctx context.Context, orderID string, amount int64) (*payment.ReserveResponse, error)
	Cancel(ctx context.Context, paymentID string) error
}

type WarehouseClient interface {
	Reserve(ctx context.Context, orderID string, items []model.OrderItem) (*warehouse.ReserveResponse, error)
	CancelReservation(ctx context.Context, reservationID string) error
}

type Service struct {
	paymentClient   PaymentClient
	warehouseClient WarehouseClient
	logger          *slog.Logger
	saver           Saver
	getter          Getter
	updater         StatusUpdater
	topic           string
}

func NewOrderService(l *slog.Logger,
	saver Saver,
	getter Getter,
	updater StatusUpdater,
	wc WarehouseClient,
	pc PaymentClient,
	topic string) *Service {
	return &Service{
		warehouseClient: wc,
		paymentClient:   pc,
		logger:          l,
		saver:           saver,
		getter:          getter,
		updater:         updater,
		topic:           topic,
	}
}
