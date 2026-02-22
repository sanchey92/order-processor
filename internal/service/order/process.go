package order

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/sanchey92/order-processor/internal/domain/model"
	"github.com/sanchey92/order-processor/pkg/saga"
)

func (s *Service) ProcessCommand(ctx context.Context, cmd *model.CreateOrderCommand, orderID string) error {
	log := s.logger.With(slog.String("order_ID", orderID))

	var paymentID, reservationID string

	orchestrator := saga.New("create-order", []saga.Step{
		// RESERVE PAYMENT
		{
			Name: "reserve-payment",
			Execute: func(ctx context.Context) error {
				resp, err := s.paymentClient.Reserve(ctx, orderID, model.NewOrder(cmd.UserID, cmd.Items).Amount)
				if err != nil {
					return fmt.Errorf("reserve-payment: %w", err)
				}
				paymentID = resp.PaymentID
				return nil
			},
			Compensate: func(ctx context.Context) error {
				if paymentID == "" {
					return nil
				}
				return s.paymentClient.Cancel(ctx, paymentID)
			},
		},
		// RESERVE INVENTORY
		{
			Name: "reserve-inventory",
			Execute: func(ctx context.Context) error {
				resp, err := s.warehouseClient.Reserve(ctx, orderID, cmd.Items)
				if err != nil {
					return fmt.Errorf("reserve-inventory: %w", err)
				}
				reservationID = resp.ReservationID
				return nil
			},
			Compensate: func(ctx context.Context) error {
				if reservationID == "" {
					return nil
				}
				return s.warehouseClient.CancelReservation(ctx, reservationID)
			},
		},
		// CONFIRM ORDER
		{
			Name: "confirm-order",
			Execute: func(ctx context.Context) error {
				event, _ := json.Marshal(&model.OrderConfirmedEvent{
					OrderID:       orderID,
					PaymentID:     paymentID,
					ReservationID: reservationID,
					ConfirmedAt:   time.Now().UTC(),
				})
				err := s.updater.UpdateStatusTx(ctx, orderID, model.OrderConfirmed, &model.OutboxMessage{
					Topic:     s.topic,
					Key:       cmd.UserID,
					EventType: "OrderConfirmed",
					Payload:   event,
					Headers:   map[string]string{"order-id": orderID},
				})
				if err != nil {
					return fmt.Errorf("confirm-order.execute: %w", err)
				}
				return nil
			},
			Compensate: func(ctx context.Context) error {
				event, _ := json.Marshal(&model.OrderCancelledEvent{
					OrderID:     orderID,
					Reason:      "saga compensation",
					FailedStep:  "confirm-order",
					CancelledAt: time.Now().UTC(),
				})
				err := s.updater.UpdateStatusTx(ctx, orderID, model.OrderCancelled, &model.OutboxMessage{
					Topic:     s.topic,
					Key:       cmd.UserID,
					EventType: "OrderCancelled",
					Payload:   event,
					Headers:   map[string]string{"order-id": orderID},
				})
				if err != nil {
					return fmt.Errorf("confirm-order.compensate: %w", err)
				}
				return nil
			},
		},
	}, log)

	result := orchestrator.Execute(ctx)
	if !result.Success {
		if result.IsPoisoned() {
			log.Error("SAGA POISONED - manual intervention required",
				slog.String("failed_step", result.FailedStep),
				slog.Any("compensation_errors", result.CompensationErrors))
		}
		return fmt.Errorf("saga failed at %q: %w", result.FailedStep, result.Error)
	}
	log.Info("order confirmed", slog.String("payment_id", paymentID), slog.String("reservation_id", reservationID))
	return nil
}
