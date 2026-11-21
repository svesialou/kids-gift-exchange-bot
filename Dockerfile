FROM golang:1.22-alpine AS builder
WORKDIR /src
ENV CGO_ENABLED=0
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN go build -trimpath -ldflags="-s -w" -o /out/bot ./cmd/bot

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
USER app
WORKDIR /app
COPY --from=builder /out/bot /app/bot
ENTRYPOINT ["/app/bot"]
