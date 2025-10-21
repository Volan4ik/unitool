run:
	go run ./cmd/bot

gen:
	sqlc generate

lint:
	golangci-lint run