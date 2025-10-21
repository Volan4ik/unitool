FROM golang:1.22-alpine AS build
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/webhook ./cmd/webhook

FROM gcr.io/distroless/base-debian12:nonroot
WORKDIR /app
COPY --from=build /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=build /etc/ssl/certs /etc/ssl/certs
COPY --from=build /bin/webhook /usr/local/bin/webhook
ENV TZ=UTC
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/usr/local/bin/webhook"]