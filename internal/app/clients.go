package app

import (
	"log/slog"

	"github.com/sanchey92/order-processor/internal/config"
	"github.com/sanchey92/order-processor/internal/http/client/payment"
	"github.com/sanchey92/order-processor/internal/http/client/warehouse"
	"github.com/sanchey92/order-processor/pkg/breaker"
)

func initClients(cfg *config.Config, logger *slog.Logger) (*payment.Client, *warehouse.Client) {
	paymentCB := breaker.New(&breaker.Config{
		Name: "payment", MaxFailures: cfg.Payment.CBMaxFailures,
		ResetTimeout: cfg.Payment.CBResetTimeout, SlowCallThreshold: cfg.Payment.CBSlowThreshold,
		IsFailure: payment.IsServerFailure,
	}, logger)

	warehouseCB := breaker.New(&breaker.Config{
		Name: "warehouse", MaxFailures: cfg.Warehouse.CBMaxFailures,
		ResetTimeout: cfg.Warehouse.CBResetTimeout, SlowCallThreshold: cfg.Warehouse.CBSlowThreshold,
		IsFailure: warehouse.IsServerFailure,
	}, logger)

	paymentClient := payment.New(cfg.Payment.BaseURL, cfg.Payment.Timeout, paymentCB)
	warehouseClient := warehouse.New(cfg.Warehouse.BaseURL, cfg.Warehouse.Timeout, warehouseCB)

	return paymentClient, warehouseClient
}
