FROM golang:1.22-alpine AS build
WORKDIR /src
RUN apk add --no-cache ca-certificates tzdata
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bot ./cmd/bot

FROM alpine:3.21
WORKDIR /app
RUN adduser -D -H -u 65532 nonroot \
    && mkdir -p /var/log/unitool /data/logs \
    && chown -R nonroot:nonroot /var/log/unitool /data/logs /app
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build /etc/ssl/certs /etc/ssl/certs
COPY --from=build /bot /bot
ENV TZ=UTC \
    LOG_FILE_PATH=/data/logs/bot.log
USER nonroot:nonroot
ENTRYPOINT ["/bot"]
