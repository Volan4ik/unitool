package config

import (
	"os"
	"strconv"
)

type Config struct {
	Port             string
	BotToken         string
	PublicBaseURL    string
	DatabaseURL      string
	DefaultFreeTries int
}

func Load() Config {
	return Config{
		Port:             getEnv("PORT", "8080"),
		BotToken:         mustEnv("BOT_TOKEN"),
		PublicBaseURL:    getEnv("PUBLIC_BASE_URL", "http://localhost:8080"),
		DatabaseURL:      mustEnv("DATABASE_URL"),
		DefaultFreeTries: getEnvInt("FREE_TRIES", 3),
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

func getEnvInt(k string, def int) int {
	if v := os.Getenv(k); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}