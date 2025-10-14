FROM golang:1.22-alpine AS build
WORKDIR /app
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -o /bin/webhook ./cmd/webhook

FROM alpine:3.20
WORKDIR /app
COPY --from=build /bin/webhook /usr/local/bin/webhook
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/webhook"]