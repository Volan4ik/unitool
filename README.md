# Unitool Bot

Telegram bot on Go with PostgreSQL, webhook delivery, retention cleanup and Prometheus metrics.

## Run

1. Configure `.env`:
   - `LOG_LEVEL` (example: `debug`, `info`, `warn`, `error`)
   - `LOG_FILE_PATH` (default: `./logs/bot.log`; leave empty to disable file logging; in Docker use `/var/log/unitool/bot.log`)
   - `LOG_ROTATE_MAX_MB` (0 disables rotation by size)
   - `LOG_ROTATE_BACKUPS` (how many rotated files to keep)
   - `LOG_DEDUPE_WINDOW_MS` (default `0`; optional suppression of exact duplicate log events inside this window)
   - `TELEGRAM_TOKEN`
   - `PROVIDER_TOKEN`
   - `YOOKASSA_ENABLED` (default `true`; set `false` to hide SBP payments)
   - `YOOKASSA_SHOP_ID` (temporary default: `test_shop_id`)
   - `YOOKASSA_SECRET_KEY` (temporary default: `test_secret_key`)
   - `YOOKASSA_RETURN_URL` (default: `https://tg-aibot-tutas9.amvera.io/yookassa/return`)
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
- `POST /yookassa/webhook`
- `GET /yookassa/return`
- `GET /metrics` (on `METRICS_ADDR`)
