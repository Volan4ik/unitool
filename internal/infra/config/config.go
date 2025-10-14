package config

import "os"

type Config struct {
	Port         string
	BotToken     string
	PublicBaseURL string
}

func Load() Config {
	return Config{
		Port:          getEnv("PORT", "8080"),
		BotToken:      mustEnv("BOT_TOKEN"),
		PublicBaseURL: getEnv("PUBLIC_BASE_URL", "http://localhost:8080"),
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
	if v := os.Getenv(k); v != "" { return v }
	return def
}