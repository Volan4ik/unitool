run:
	go run ./cmd/webhook

build:
	go build -o bin/webhook ./cmd/webhook

docker-build:
	docker build -t unitool:dev .

docker-run:
	docker run --rm -p 8080:8080 --env-file .env unitool:dev

mod:
	go mod tidy