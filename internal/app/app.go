package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"golang.org/x/sync/errgroup"

	"github.com/sanchey92/order-processor/internal/config"
	"github.com/sanchey92/order-processor/internal/http/client/payment"
	"github.com/sanchey92/order-processor/internal/http/client/warehouse"
	"github.com/sanchey92/order-processor/internal/http/handlers"
	"github.com/sanchey92/order-processor/internal/http/middlewares"
	kafkaHandler "github.com/sanchey92/order-processor/internal/kafka/handler"
	"github.com/sanchey92/order-processor/internal/service/order"
	"github.com/sanchey92/order-processor/internal/storage/pg"
	"github.com/sanchey92/order-processor/pkg/breaker"
	customKafka "github.com/sanchey92/order-processor/pkg/kafka"
	"github.com/sanchey92/order-processor/pkg/outbox"
)

type App struct {
	logger     *slog.Logger
	pgStorage  *pg.Storage
	producer   *customKafka.Producer
	consumer   *customKafka.Consumer
	httpServer *http.Server
	relay      *outbox.Relay
}

func New(cfg *config.Config) (*App, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config required")
	}

	ctx := context.Background()

	// Logger initialisation
	logger := newLogger(cfg.App.LogLevel, cfg.App.Name)
	slog.SetDefault(logger)
	logger.Info("initialising", slog.String("service", cfg.App.Name))

	// PgStorage initialisation
	pgConfig := &pg.StorageConfig{
		DSN:             cfg.Postgres.DSN,
		MaxConns:        cfg.Postgres.MaxConns,
		MinConns:        cfg.Postgres.MinConns,
		MaxConnLife:     cfg.Postgres.MaxConnLifetime,
		MaxConnIdleTime: cfg.Postgres.MaxConnIdleTime,
	}

	pgStorage, err := pg.NewPGStorage(ctx, logger, pgConfig)
	if err != nil {
		return nil, fmt.Errorf("app creation: %w", err)
	}

	logger.Info("postgres connected")

	// Mock services initialization
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

	// Order Service initialization
	orderService := order.NewOrderService(logger, pgStorage, pgStorage, pgStorage, warehouseClient,
		paymentClient, cfg.Kafka.EventTopic)

	// HTTP Server initialization
	r := chi.NewRouter()
	r.Use(middlewares.Recovery(logger))
	r.Use(middleware.RequestID)

	r.Route("/api/v1/orders", func(r chi.Router) {
		r.Get("/{id}", handlers.GetByID(orderService))
		r.Post("/", handlers.Create(orderService))
	})

	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.HTTP.Port),
		Handler:      r,
		ReadTimeout:  cfg.HTTP.ReadTimeout,
		WriteTimeout: cfg.HTTP.WriteTimeout,
	}

	// Kafka producer initialization
	producer, err := customKafka.NewProducer(&customKafka.ProducerConfig{
		Brokers:     cfg.Kafka.Brokers,
		Acks:        cfg.Kafka.Acks,
		LingerMs:    cfg.Kafka.LingerMs,
		Compression: cfg.Kafka.Compression,
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("create producer: %w", err)
	}

	// Outbox relay initialization
	relay := outbox.NewRelay(
		pgStorage,
		producer,
		logger,
		cfg.Outbox.BatchSize,
		cfg.Outbox.PollInterval,
	)

	// Kafka consumer and handler initialization
	rawHandler := kafkaHandler.NewKafkaHandler(orderService, logger)

	handler := kafkaHandler.BuildHandler(
		rawHandler.Handle,
		pgStorage,
		pgStorage,
		producer,
		cfg.Kafka.DLQTopic,
		cfg.Kafka.MaxRetries,
		logger)

	consumer, err := customKafka.NewConsumer(&customKafka.ConsumerConfig{
		Topics:            []string{cfg.Kafka.CommandTopic},
		Brokers:           cfg.Kafka.Brokers,
		ConsumerGroup:     cfg.Kafka.ConsumerGroup,
		OffsetReset:       "earliest",
		SessionTimeoutMs:  cfg.Kafka.SessionTimeoutMs,
		MaxPollInterval:   cfg.Kafka.MaxPollInterval,
		PartitionStrategy: "cooperative-sticky",
		ChannelBufferSize: 256,
	}, handler, logger)
	if err != nil {
		producer.Close()
		pgStorage.Close()
		return nil, fmt.Errorf("create consumer: %w", err)
	}

	logger.Info("application initialized")

	return &App{
		logger:     logger,
		pgStorage:  pgStorage,
		httpServer: srv,
		producer:   producer,
		consumer:   consumer,
		relay:      relay,
	}, nil
}

func (a *App) Run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	g, gCtx := errgroup.WithContext(ctx)

	g.Go(func() error {
		a.logger.Info("http server starting")
		if err := a.httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("http server: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		a.logger.Info("kafka consumer started")
		if err := a.consumer.Run(gCtx); err != nil {
			return fmt.Errorf("consumer run error: %w", err)
		}
		return nil
	})

	g.Go(func() error {
		return a.relay.Run(gCtx)
	})

	g.Go(func() error {
		<-gCtx.Done()
		a.logger.Info("shutdown signal received")
		return a.shutdown()
	})

	err := g.Wait()

	a.producer.Close()
	a.pgStorage.Close()
	a.logger.Info("resources released")

	return err
}

func (a *App) shutdown() error {
	a.logger.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := a.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}

	a.logger.Info("http server stopped")
	return nil
}

func newLogger(level, service string) *slog.Logger {
	var lvl slog.Level
	switch level {
	case "info":
		lvl = slog.LevelInfo
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})).
		With(slog.String("service", service))
}
