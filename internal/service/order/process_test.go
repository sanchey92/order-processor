package order

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/sanchey92/order-processor/internal/domain/model"
	"github.com/sanchey92/order-processor/internal/http/client/payment"
	"github.com/sanchey92/order-processor/internal/http/client/warehouse"
)

// --- mocks ---

type paymentMock struct {
	reserveResp *payment.ReserveResponse
	reserveErr  error
	cancelErr   error

	reserveCalled int
	cancelCalled  int
	cancelledIDs  []string
}

func (m *paymentMock) Reserve(_ context.Context, _ string, _ int64) (*payment.ReserveResponse, error) {
	m.reserveCalled++
	return m.reserveResp, m.reserveErr
}

func (m *paymentMock) Cancel(_ context.Context, paymentID string) error {
	m.cancelCalled++
	m.cancelledIDs = append(m.cancelledIDs, paymentID)
	return m.cancelErr
}

type warehouseMock struct {
	reserveResp *warehouse.ReserveResponse
	reserveErr  error
	cancelErr   error

	reserveCalled int
	cancelCalled  int
	cancelledIDs  []string
}

func (m *warehouseMock) Reserve(_ context.Context, _ string, _ []model.OrderItem) (*warehouse.ReserveResponse, error) {
	m.reserveCalled++
	return m.reserveResp, m.reserveErr
}

func (m *warehouseMock) CancelReservation(_ context.Context, reservationID string) error {
	m.cancelCalled++
	m.cancelledIDs = append(m.cancelledIDs, reservationID)
	return m.cancelErr
}

type updaterMock struct {
	err      error
	calls    int
	statuses []model.OrderStatus
}

func (m *updaterMock) UpdateStatusTx(_ context.Context, _ string, status model.OrderStatus, _ *model.OutboxMessage) error {
	m.calls++
	m.statuses = append(m.statuses, status)
	return m.err
}

// --- helpers ---

var discardLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

func newTestService(pm PaymentClient, wm WarehouseClient, um StatusUpdater) *Service {
	return NewOrderService(discardLogger, nil, nil, um, wm, pm, "test-topic")
}

func testCmd() *model.CreateOrderCommand {
	return &model.CreateOrderCommand{
		UserID: "user-1",
		Items:  []model.OrderItem{{ProductID: "p1", Quantity: 2, Price: 500}},
	}
}

// --- tests ---

func TestProcessCommand_HappyPath(t *testing.T) {
	pm := &paymentMock{reserveResp: &payment.ReserveResponse{PaymentID: "pay-1"}}
	wm := &warehouseMock{reserveResp: &warehouse.ReserveResponse{ReservationID: "res-1"}}
	um := &updaterMock{}

	svc := newTestService(pm, wm, um)
	err := svc.ProcessCommand(context.Background(), testCmd(), "order-1")

	if err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
	if pm.reserveCalled != 1 {
		t.Errorf("payment.Reserve: want 1 call, got %d", pm.reserveCalled)
	}
	if wm.reserveCalled != 1 {
		t.Errorf("warehouse.Reserve: want 1 call, got %d", wm.reserveCalled)
	}
	if um.calls != 1 {
		t.Errorf("UpdateStatusTx: want 1 call, got %d", um.calls)
	}
	if um.statuses[0] != model.OrderConfirmed {
		t.Errorf("status: want %s, got %s", model.OrderConfirmed, um.statuses[0])
	}
	if pm.cancelCalled != 0 {
		t.Errorf("payment.Cancel: want 0, got %d", pm.cancelCalled)
	}
	if wm.cancelCalled != 0 {
		t.Errorf("warehouse.Cancel: want 0, got %d", wm.cancelCalled)
	}
}

func TestProcessCommand_PaymentReserveFails(t *testing.T) {
	pm := &paymentMock{reserveErr: errors.New("payment unavailable")}
	wm := &warehouseMock{}
	um := &updaterMock{}

	svc := newTestService(pm, wm, um)
	err := svc.ProcessCommand(context.Background(), testCmd(), "order-1")

	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `"reserve-payment"`) {
		t.Errorf("error should reference failed step, got: %s", err.Error())
	}
	// first step failed → no compensation
	if pm.cancelCalled != 0 {
		t.Errorf("payment.Cancel: want 0, got %d", pm.cancelCalled)
	}
	// subsequent steps not executed
	if wm.reserveCalled != 0 {
		t.Errorf("warehouse.Reserve: want 0, got %d", wm.reserveCalled)
	}
	if um.calls != 0 {
		t.Errorf("UpdateStatusTx: want 0, got %d", um.calls)
	}
}

func TestProcessCommand_WarehouseReserveFails_CompensatesPayment(t *testing.T) {
	pm := &paymentMock{reserveResp: &payment.ReserveResponse{PaymentID: "pay-1"}}
	wm := &warehouseMock{reserveErr: errors.New("out of stock")}
	um := &updaterMock{}

	svc := newTestService(pm, wm, um)
	err := svc.ProcessCommand(context.Background(), testCmd(), "order-1")

	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `"reserve-inventory"`) {
		t.Errorf("error should reference failed step, got: %s", err.Error())
	}
	// payment compensated with correct ID
	if pm.cancelCalled != 1 {
		t.Fatalf("payment.Cancel: want 1, got %d", pm.cancelCalled)
	}
	if pm.cancelledIDs[0] != "pay-1" {
		t.Errorf("cancelled paymentID: want pay-1, got %s", pm.cancelledIDs[0])
	}
	// warehouse not compensated (failed step is not in completed list)
	if wm.cancelCalled != 0 {
		t.Errorf("warehouse.Cancel: want 0, got %d", wm.cancelCalled)
	}
	// confirm not reached
	if um.calls != 0 {
		t.Errorf("UpdateStatusTx: want 0, got %d", um.calls)
	}
}

func TestProcessCommand_ConfirmFails_CompensatesBoth(t *testing.T) {
	pm := &paymentMock{reserveResp: &payment.ReserveResponse{PaymentID: "pay-1"}}
	wm := &warehouseMock{reserveResp: &warehouse.ReserveResponse{ReservationID: "res-1"}}
	um := &updaterMock{err: errors.New("db error")}

	svc := newTestService(pm, wm, um)
	err := svc.ProcessCommand(context.Background(), testCmd(), "order-1")

	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), `"confirm-order"`) {
		t.Errorf("error should reference failed step, got: %s", err.Error())
	}
	// warehouse compensated (reverse order: warehouse first, then payment)
	if wm.cancelCalled != 1 {
		t.Fatalf("warehouse.Cancel: want 1, got %d", wm.cancelCalled)
	}
	if wm.cancelledIDs[0] != "res-1" {
		t.Errorf("cancelled reservationID: want res-1, got %s", wm.cancelledIDs[0])
	}
	// payment compensated
	if pm.cancelCalled != 1 {
		t.Fatalf("payment.Cancel: want 1, got %d", pm.cancelCalled)
	}
	if pm.cancelledIDs[0] != "pay-1" {
		t.Errorf("cancelled paymentID: want pay-1, got %s", pm.cancelledIDs[0])
	}
}

func TestProcessCommand_CompensationFails_Poisoned(t *testing.T) {
	pm := &paymentMock{
		reserveResp: &payment.ReserveResponse{PaymentID: "pay-1"},
		cancelErr:   errors.New("payment cancel failed"),
	}
	wm := &warehouseMock{reserveErr: errors.New("out of stock")}
	um := &updaterMock{}

	svc := newTestService(pm, wm, um)
	err := svc.ProcessCommand(context.Background(), testCmd(), "order-1")

	if err == nil {
		t.Fatal("expected error")
	}
	// payment Cancel was attempted even though it fails
	if pm.cancelCalled != 1 {
		t.Errorf("payment.Cancel: want 1, got %d", pm.cancelCalled)
	}
}

func TestProcessCommand_ContextCancelled(t *testing.T) {
	pm := &paymentMock{}
	wm := &warehouseMock{}
	um := &updaterMock{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	svc := newTestService(pm, wm, um)
	err := svc.ProcessCommand(ctx, testCmd(), "order-1")

	if err == nil {
		t.Fatal("expected error")
	}
	// no steps executed
	if pm.reserveCalled != 0 {
		t.Errorf("payment.Reserve: want 0, got %d", pm.reserveCalled)
	}
	if wm.reserveCalled != 0 {
		t.Errorf("warehouse.Reserve: want 0, got %d", wm.reserveCalled)
	}
	if um.calls != 0 {
		t.Errorf("UpdateStatusTx: want 0, got %d", um.calls)
	}
}

func TestProcessCommand_ConfirmFails_AllCompensationsFail_Poisoned(t *testing.T) {
	pm := &paymentMock{
		reserveResp: &payment.ReserveResponse{PaymentID: "pay-1"},
		cancelErr:   errors.New("payment cancel failed"),
	}
	wm := &warehouseMock{
		reserveResp: &warehouse.ReserveResponse{ReservationID: "res-1"},
		cancelErr:   errors.New("warehouse cancel failed"),
	}
	um := &updaterMock{err: errors.New("db error")}

	svc := newTestService(pm, wm, um)
	err := svc.ProcessCommand(context.Background(), testCmd(), "order-1")

	if err == nil {
		t.Fatal("expected error")
	}
	// both compensations attempted despite failures
	if wm.cancelCalled != 1 {
		t.Errorf("warehouse.Cancel: want 1, got %d", wm.cancelCalled)
	}
	if pm.cancelCalled != 1 {
		t.Errorf("payment.Cancel: want 1, got %d", pm.cancelCalled)
	}
}
