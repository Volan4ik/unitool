# Unitool Bot

Telegram bot on Go with PostgreSQL, webhook delivery, retention cleanup and Prometheus metrics.

## Run

1. Configure `.env`:
   - `LOG_LEVEL` (example: `debug`, `info`, `warn`, `error`)
   - `LOG_FILE_PATH` (default: `./logs/bot.log`; leave empty to disable file logging)
   - `LOG_ROTATE_MAX_MB` (0 disables rotation by size)
   - `LOG_ROTATE_BACKUPS` (how many rotated files to keep)
   - `LOG_DEDUPE_WINDOW_MS` (suppresses exact duplicate log events inside this window)
   - `TELEGRAM_TOKEN`
   - `PROVIDER_TOKEN`
   - `COMET_API_KEY`
   - `DATABASE_URL`
   - `WEBHOOK_URL` (absolute URL with non-root path, example: `https://bot.example.com/tg/webhook`)
   - `WEBHOOK_SECRET_TOKEN` (any strong random string; must match Telegram webhook secret)
   - `START_GUIDE_ANIMATION` (optional; Telegram `file_id`, `https://...` URL, or `file:///abs/path/to/guide.gif`)
2. Start dependencies:
   - `docker compose -f deploy/docker-compose.yml up -d db`
3. Run bot:
   - `go run ./cmd/bot`

## Endpoints

- `GET /healthz`
- `GET /readyz`
- `GET /metrics` (on `METRICS_ADDR`)
