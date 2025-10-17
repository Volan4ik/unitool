run:
	go run ./cmd/webhook

build:
	go build -o bin/webhook ./cmd/webhook

docker-build:
	docker build -t ai-telebot:dev .

docker-run:
	docker run --rm -p 8080:8080 --env-file .env ai-telebot:dev

mod:
	go mod tidy

pg-up:
	docker run --name ai-bot-pg -e POSTGRES_PASSWORD=pass -e POSTGRES_USER=user -e POSTGRES_DB=ai_bot -p 5432:5432 -d postgres:16

pg-down:
	docker rm -f ai-bot-pg || true