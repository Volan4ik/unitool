package config

import (
	"github.com/kelseyhightower/envconfig"
	"time"
)

type Config struct {
	AppEnv          string        `envconfig:"APP_ENV" default:"dev"`
	HTTPAddr        string        `envconfig:"HTTP_ADDR" default:":8080"`
	MetricsAddr     string        `envconfig:"METRICS_ADDR" default:":9090"`
	TelegramToken   string        `envconfig:"TELEGRAM_TOKEN" required:"true"`
	ProviderToken   string        `envconfig:"PROVIDER_TOKEN" required:"true"` // BotFather payments token
	WebhookURL      string        `envconfig:"WEBHOOK_URL" default:""`
	DBURL           string        `envconfig:"DATABASE_URL" required:"true"`
	MaxWorkers      int           `envconfig:"MAX_WORKERS" default:"64"`
	QueueBuffer     int           `envconfig:"QUEUE_BUFFER" default:"1024"`
	RateRPS         int           `envconfig:"RATE_RPS" default:"200"`
	RateBurst       int           `envconfig:"RATE_BURST" default:"400"`
	ShutdownTimeout time.Duration `envconfig:"SHUTDOWN_TIMEOUT" default:"10s"`
	AutoMigrate     bool          `envconfig:"AUTO_MIGRATE" default:"true"`
	CometBase       string        `envconfig:"COMET_API_BASE" default:"https://api.cometapi.com"`
	CometKey        string        `envconfig:"COMET_API_KEY" required:"true"`
	CometTimeout    time.Duration `envconfig:"COMET_TIMEOUT" default:"25s"`
	// Provider rate limiting
	CometRPS   int `envconfig:"COMET_RPS" default:"8"`
	CometBurst int `envconfig:"COMET_BURST" default:"8"`
	// Telegram streaming edit throttling
	TGEditThrottleMs int `envconfig:"TG_EDIT_THROTTLE_MS" default:"900"`
	TGEditMaxPerMin  int `envconfig:"TG_EDIT_MAX_PER_MIN" default:"40"`
	// Async media generation workers
	JobWorkers      int           `envconfig:"JOB_WORKERS" default:"4"`
	JobPollMs       int           `envconfig:"JOB_POLL_MS" default:"700"`
	MediaGenTimeout time.Duration `envconfig:"MEDIA_GEN_TIMEOUT" default:"120s"`
	// Weekly free text generations
	WeeklyTextGenerations int    `envconfig:"WEEKLY_TEXT_GENERATIONS" default:"100"`
	WeeklyCronAtUTC       string `envconfig:"WEEKLY_CRON_AT_UTC" default:"Mon 03:00"`
	// OpenAI Moderation API
	ModerationEnabled bool          `envconfig:"MODERATION_ENABLED" default:"true"`
	OpenAIBase        string        `envconfig:"OPENAI_API_BASE" default:"https://api.openai.com"`
	OpenAIKey         string        `envconfig:"OPENAI_API_KEY" default:""`
	ModerationModel   string        `envconfig:"MODERATION_MODEL" default:"omni-moderation-latest"`
	ModerationTimeout time.Duration `envconfig:"MODERATION_TIMEOUT" default:"12s"`
}

func Load() (Config, error) {
	var c Config
	if err := envconfig.Process("", &c); err != nil {
		return c, err
	}
	return c, nil
}
