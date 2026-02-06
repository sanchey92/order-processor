package config

import (
	"fmt"
	"os"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
)

type Config struct {
	App       App       `yaml:"app"`
	HTTP      HTTP      `yaml:"http"`
	Kafka     Kafka     `yaml:"kafka"`
	Postgres  Postgres  `yaml:"postgres"`
	Outbox    Outbox    `yaml:"outbox"`
	Payment   External  `yaml:"payment"`
	Warehouse External  `yaml:"warehouse"`
	Telemetry Telemetry `yaml:"telemetry"`
}

type App struct {
	Name     string `yaml:"name"      env:"APP_NAME"      env-default:"order-processor"`
	LogLevel string `yaml:"log_level" env:"APP_LOG_LEVEL"  env-default:"info"`
}

type HTTP struct {
	Port            int           `yaml:"port"             env:"HTTP_PORT"             env-default:"8080"`
	ReadTimeout     time.Duration `yaml:"read_timeout"     env:"HTTP_READ_TIMEOUT"     env-default:"10s"`
	WriteTimeout    time.Duration `yaml:"write_timeout"    env:"HTTP_WRITE_TIMEOUT"    env-default:"10s"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout" env:"HTTP_SHUTDOWN_TIMEOUT" env-default:"15s"`
}

type Kafka struct {
	Brokers          string `yaml:"brokers"              env:"KAFKA_BROKERS"              env-default:"localhost:29092"`
	ConsumerGroup    string `yaml:"consumer_group"       env:"KAFKA_CONSUMER_GROUP"       env-default:"order-processor"`
	CommandTopic     string `yaml:"command_topic"        env:"KAFKA_COMMAND_TOPIC"        env-default:"order-commands"`
	EventTopic       string `yaml:"event_topic"          env:"KAFKA_EVENT_TOPIC"          env-default:"order-events"`
	DLQTopic         string `yaml:"dlq_topic"            env:"KAFKA_DLQ_TOPIC"            env-default:"order-commands.dlq"`
	Acks             string `yaml:"acks"                 env:"KAFKA_ACKS"                 env-default:"all"`
	LingerMs         int    `yaml:"linger_ms"            env:"KAFKA_LINGER_MS"            env-default:"10"`
	Compression      string `yaml:"compression"          env:"KAFKA_COMPRESSION"          env-default:"lz4"`
	SessionTimeoutMs int    `yaml:"session_timeout_ms"   env:"KAFKA_SESSION_TIMEOUT_MS"   env-default:"30000"`
	MaxPollInterval  int    `yaml:"max_poll_interval_ms" env:"KAFKA_MAX_POLL_INTERVAL_MS" env-default:"300000"`
	MaxRetries       int    `yaml:"max_retries"          env:"KAFKA_MAX_RETRIES"          env-default:"3"`
}

type Postgres struct {
	DSN             string        `yaml:"dsn"               env:"POSTGRES_DSN"               env-required:"true"`
	MaxConns        int32         `yaml:"max_conns"         env:"POSTGRES_MAX_CONNS"         env-default:"20"`
	MinConns        int32         `yaml:"min_conns"         env:"POSTGRES_MIN_CONNS"         env-default:"5"`
	MaxConnLifetime time.Duration `yaml:"max_conn_lifetime" env:"POSTGRES_MAX_CONN_LIFETIME" env-default:"30m"`
	MaxConnIdleTime time.Duration `yaml:"max_conn_idle_time" env:"POSTGRES_MAX_CONN_IDLE_TIME" env-default:"5m"`
}

type Outbox struct {
	BatchSize    int           `yaml:"batch_size"    env:"OUTBOX_BATCH_SIZE"    env-default:"100"`
	PollInterval time.Duration `yaml:"poll_interval" env:"OUTBOX_POLL_INTERVAL" env-default:"500ms"`
}

type External struct {
	BaseURL         string        `yaml:"base_url"         env-required:"true"`
	Timeout         time.Duration `yaml:"timeout"          env-default:"3s"`
	CBMaxFailures   int           `yaml:"cb_max_failures"  env-default:"3"`
	CBResetTimeout  time.Duration `yaml:"cb_reset_timeout" env-default:"15s"`
	CBSlowThreshold time.Duration `yaml:"cb_slow_threshold" env-default:"2s"`
}

type Telemetry struct {
	OTELEndpoint string  `yaml:"otel_endpoint" env:"TELEMETRY_OTEL_ENDPOINT" env-default:"localhost:4317"`
	SampleRate   float64 `yaml:"sample_rate"   env:"TELEMETRY_SAMPLE_RATE"   env-default:"0.1"`
	MetricsPort  int     `yaml:"metrics_port"  env:"TELEMETRY_METRICS_PORT"  env-default:"9090"`
}

func MustLoad(path string) *Config {
	if path == "" {
		panic("Config path is not set")
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		panic(fmt.Sprintf("file does not exists: %s: %v", path, err))
	}

	var cfg Config

	if err := cleanenv.ReadConfig(path, &cfg); err != nil {
		panic(fmt.Sprintf("reading config: %s: %v", path, err))
	}

	return &cfg
}
