package config

import (
	"time"
	"github.com/kelseyhightower/envconfig"
)

type Config struct {
	AppEnv           string `envconfig:"APP_ENV" default:"dev"`
	HTTPAddr         string `envconfig:"HTTP_ADDR" default:":8080"`
	MetricsAddr      string `envconfig:"METRICS_ADDR" default:":9090"`
	TelegramToken    string `envconfig:"TELEGRAM_TOKEN" required:"true"`
	ProviderToken    string `envconfig:"PROVIDER_TOKEN" required:"true"` // BotFather payments token
	WebhookURL       string `envconfig:"WEBHOOK_URL" default:""`
	DBURL            string `envconfig:"DATABASE_URL" required:"true"`
	MaxWorkers       int    `envconfig:"MAX_WORKERS" default:"64"`
	QueueBuffer      int    `envconfig:"QUEUE_BUFFER" default:"1024"`
	RateRPS          int    `envconfig:"RATE_RPS" default:"200"`
	RateBurst        int    `envconfig:"RATE_BURST" default:"400"`
	FreeText         int    `envconfig:"FREE_TEXT" default:"10"`
	FreeSearch       int    `envconfig:"FREE_SEARCH" default:"10"`
	FreeImage        int    `envconfig:"FREE_IMAGE" default:"3"`
	FreeVideo        int    `envconfig:"FREE_VIDEO" default:"1"`
	MonthlyCronAtUTC string `envconfig:"MONTHLY_CRON_AT_UTC" default:"03:00"`
    ShutdownTimeout  time.Duration `envconfig:"SHUTDOWN_TIMEOUT" default:"10s"`
    AutoMigrate      bool          `envconfig:"AUTO_MIGRATE" default:"true"`
}

func Load() (Config, error) {
	var c Config
	if err := envconfig.Process("", &c); err != nil {
		return c, err
	}
	return c, nil
}
