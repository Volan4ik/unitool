# Unitool Bot

Telegram bot on Go with PostgreSQL, webhook delivery, retention cleanup and Prometheus metrics.

## Run

1. Configure `.env`:
   - `TELEGRAM_TOKEN`
   - `PROVIDER_TOKEN`
   - `COMET_API_KEY`
   - `DATABASE_URL`
   - `WEBHOOK_URL` (absolute URL with non-root path, example: `https://bot.example.com/tg/webhook`)
2. Start dependencies:
   - `docker compose -f deploy/docker-compose.yml up -d db`
3. Run bot:
   - `go run ./cmd/bot`

## Endpoints

- `GET /healthz`
- `GET /readyz`
- `GET /metrics` (on `METRICS_ADDR`)
