package config

import (
	"fmt"
	"github.com/kelseyhightower/envconfig"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	AppEnv            string        `envconfig:"APP_ENV" default:"dev"`
	HTTPAddr          string        `envconfig:"HTTP_ADDR" default:":8080"`
	MetricsAddr       string        `envconfig:"METRICS_ADDR" default:":9090"`
	MetricsToken      string        `envconfig:"METRICS_TOKEN" default:""`
	LogLevel          string        `envconfig:"LOG_LEVEL" default:"info"`
	LogFilePath       string        `envconfig:"LOG_FILE_PATH" default:"./logs/bot.log"`
	LogRotateMaxMB    int           `envconfig:"LOG_ROTATE_MAX_MB" default:"50"`
	LogRotateBackups  int           `envconfig:"LOG_ROTATE_BACKUPS" default:"7"`
	LogDedupeWindowMs int           `envconfig:"LOG_DEDUPE_WINDOW_MS" default:"0"`
	TelegramToken     string        `envconfig:"TELEGRAM_TOKEN" required:"true"`
	ProviderToken     string        `envconfig:"PROVIDER_TOKEN" required:"true"` // BotFather payments token
	WebhookURL        string        `envconfig:"WEBHOOK_URL" required:"true"`
	WebhookSecret     string        `envconfig:"WEBHOOK_SECRET_TOKEN" required:"true"`
	WebhookMaxBody    int64         `envconfig:"WEBHOOK_MAX_BODY_BYTES" default:"1048576"`
	WebhookStaleSec   int           `envconfig:"WEBHOOK_UPDATE_STALE_SEC" default:"300"`
	YooKassaEnabled   bool          `envconfig:"YOOKASSA_ENABLED" default:"true"`
	YooKassaShopID    string        `envconfig:"YOOKASSA_SHOP_ID"`
	YooKassaSecretKey string        `envconfig:"YOOKASSA_SECRET_KEY"`
	YooKassaReturnURL string        `envconfig:"YOOKASSA_RETURN_URL"`
	YooKassaAPIBase   string        `envconfig:"YOOKASSA_API_BASE" default:"https://api.yookassa.ru"`
	DBURL             string        `envconfig:"DATABASE_URL" required:"true"`
	DBMaxConns        int           `envconfig:"DB_MAX_CONNS" default:"120"`
	DBMinConns        int           `envconfig:"DB_MIN_CONNS" default:"20"`
	MaxWorkers        int           `envconfig:"MAX_WORKERS" default:"64"`
	QueueBuffer       int           `envconfig:"QUEUE_BUFFER" default:"1024"`
	AutoMigrate       bool          `envconfig:"AUTO_MIGRATE" default:"true"`
	BusinessTimezone  string        `envconfig:"BUSINESS_TIMEZONE" default:"Europe/Moscow"`
	CometBase         string        `envconfig:"COMET_API_BASE" default:"https://api.cometapi.com"`
	CometKey          string        `envconfig:"COMET_API_KEY" required:"true"`
	CometTimeout      time.Duration `envconfig:"COMET_TIMEOUT" default:"25s"`
	// Provider rate limiting
	CometRPS   int `envconfig:"COMET_RPS" default:"8"`
	CometBurst int `envconfig:"COMET_BURST" default:"8"`
	// Telegram streaming edit throttling
	TGEditThrottleMs int `envconfig:"TG_EDIT_THROTTLE_MS" default:"900"`
	TGEditMaxPerMin  int `envconfig:"TG_EDIT_MAX_PER_MIN" default:"40"`
	// Optional start guide animation source for /start.
	// Accepts Telegram file_id, HTTP(S) URL, or local path prefixed with file://
	StartGuideAnimation string `envconfig:"START_GUIDE_ANIMATION" default:""`
	// Async media generation workers
	JobWorkers      int           `envconfig:"JOB_WORKERS" default:"4"`
	JobPollMs       int           `envconfig:"JOB_POLL_MS" default:"700"`
	MediaGenTimeout time.Duration `envconfig:"MEDIA_GEN_TIMEOUT" default:"120s"`
	ImageGenTimeout time.Duration `envconfig:"IMAGE_GEN_TIMEOUT" default:"900s"`
	VideoGenTimeout time.Duration `envconfig:"VIDEO_GEN_TIMEOUT" default:"900s"`
	// OpenAI Moderation API
	ModerationEnabled bool          `envconfig:"MODERATION_ENABLED" default:"false"`
	OpenAIBase        string        `envconfig:"OPENAI_API_BASE" default:"https://api.openai.com"`
	OpenAIKey         string        `envconfig:"OPENAI_API_KEY" default:""`
	ModerationModel   string        `envconfig:"MODERATION_MODEL" default:"omni-moderation-latest"`
	ModerationTimeout time.Duration `envconfig:"MODERATION_TIMEOUT" default:"12s"`
	// Retention cleanup job
	RetentionEnabled              bool   `envconfig:"RETENTION_ENABLED" default:"true"`
	RetentionDailyAtUTC           string `envconfig:"RETENTION_DAILY_AT_UTC" default:"04:10"`
	RetentionKeepChatDays         int    `envconfig:"RETENTION_KEEP_CHAT_DAYS" default:"60"`
	RetentionKeepCreditLedgerDays int    `envconfig:"RETENTION_KEEP_CREDIT_LEDGER_DAYS" default:"1080"`
	RetentionKeepRequestDays      int    `envconfig:"RETENTION_KEEP_REQUEST_DAYS" default:"60"`
	RetentionKeepUpdatesDays      int    `envconfig:"RETENTION_KEEP_TELEGRAM_UPDATES_DAYS" default:"30"`
	AdminIDs                      string `envconfig:"ADMIN_IDS" default:""`
}

func Load() (Config, error) {
	var c Config
	if err := envconfig.Process("", &c); err != nil {
		return c, err
	}
	if c.WebhookStaleSec <= 0 {
		return c, fmt.Errorf("WEBHOOK_UPDATE_STALE_SEC must be > 0")
	}
	if c.LogRotateMaxMB < 0 {
		return c, fmt.Errorf("LOG_ROTATE_MAX_MB must be >= 0")
	}
	if c.LogRotateBackups < 0 {
		return c, fmt.Errorf("LOG_ROTATE_BACKUPS must be >= 0")
	}
	if c.LogDedupeWindowMs < 0 {
		return c, fmt.Errorf("LOG_DEDUPE_WINDOW_MS must be >= 0")
	}
	return c, nil
}

func (c Config) ParseAdminIDs() ([]int64, error) {
	raw := strings.TrimSpace(c.AdminIDs)
	if raw == "" {
		return nil, nil
	}
	items := strings.Split(raw, ",")
	out := make([]int64, 0, len(items))
	for _, item := range items {
		v := strings.TrimSpace(item)
		if v == "" {
			continue
		}
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid ADMIN_IDS value %q: %w", v, err)
		}
		out = append(out, id)
	}
	return out, nil
}
