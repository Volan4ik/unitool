package config

import (
	"os"
	"strings"
)

type Config struct {
	Port           string
	BotToken       string
	PublicBaseURL  string
	DatabaseURL    string
	ReturnURL      string
	YookassaShopID string
	YookassaSecret string
	MigrateOnStart bool
}

func Load() Config {
	return Config{
		Port:           getEnv("PORT", "8080"),
		BotToken:       mustEnv("BOT_TOKEN"),
		PublicBaseURL:  getEnv("PUBLIC_BASE_URL", "http://localhost:8080"),
		DatabaseURL:    mustEnv("DATABASE_URL"),
		ReturnURL:      getEnv("RETURN_URL", ""),
		YookassaShopID: getEnv("YOOKASSA_SHOP_ID", ""),
		YookassaSecret: getEnv("YOOKASSA_SECRET", ""),
		MigrateOnStart: getEnvBool("MIGRATE_ON_START"),
	}
}

func mustEnv(k string) string {
	v := os.Getenv(k)
	if v == "" {
		panic("missing env: " + k)
	}
	return v
}

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getEnvBool(k string) bool {
	v := os.Getenv(k)
	if v == "" {
		return false
	}
	switch strings.ToLower(v) {
	case "1", "true", "t", "yes", "y", "on":
		return true
	default:
		return false
	}
}
