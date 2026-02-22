package app

import (
	"log/slog"

	"github.com/sanchey92/order-processor/internal/config"
	kafkaHandler "github.com/sanchey92/order-processor/internal/kafka/handler"
	"github.com/sanchey92/order-processor/internal/service/order"
	"github.com/sanchey92/order-processor/internal/storage/pg"
	customKafka "github.com/sanchey92/order-processor/pkg/kafka"
	"github.com/sanchey92/order-processor/pkg/outbox"
)

func initProducer(cfg config.Kafka, logger *slog.Logger) (*customKafka.Producer, error) {
	return customKafka.NewProducer(&customKafka.ProducerConfig{
		Brokers:     cfg.Brokers,
		Acks:        cfg.Acks,
		LingerMs:    cfg.LingerMs,
		Compression: cfg.Compression,
	}, logger)
}

func initConsumer(
	cfg config.Kafka,
	pgStorage *pg.Storage,
	producer *customKafka.Producer,
	orderService *order.Service,
	logger *slog.Logger,
) (*customKafka.Consumer, error) {
	rawHandler := kafkaHandler.NewKafkaHandler(orderService, logger)

	handler := kafkaHandler.BuildHandler(
		rawHandler.Handle,
		pgStorage,
		pgStorage,
		producer,
		cfg.DLQTopic,
		cfg.MaxRetries,
		logger,
	)

	return customKafka.NewConsumer(&customKafka.ConsumerConfig{
		Topics:            []string{cfg.CommandTopic},
		Brokers:           cfg.Brokers,
		ConsumerGroup:     cfg.ConsumerGroup,
		OffsetReset:       "earliest",
		SessionTimeoutMs:  cfg.SessionTimeoutMs,
		MaxPollInterval:   cfg.MaxPollInterval,
		PartitionStrategy: "cooperative-sticky",
		ChannelBufferSize: 256,
	}, handler, logger)
}

func initRelay(
	cfg config.Outbox,
	pgStorage *pg.Storage,
	producer *customKafka.Producer,
	logger *slog.Logger,
) *outbox.Relay {
	return outbox.NewRelay(pgStorage, producer, logger, cfg.BatchSize, cfg.PollInterval)
}
