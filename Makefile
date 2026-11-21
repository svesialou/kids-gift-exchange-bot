TEST_MYSQL_DSN ?= root:secret@tcp(127.0.0.1:33306)/

.PHONY: build vet test test-db up down

build:
	go build -o bin/bot ./cmd/bot

vet:
	go vet ./...

# Unit tests only; store tests are skipped without TEST_MYSQL_DSN.
test:
	go test ./...

# Full suite against the MySQL from docker-compose (make up first).
test-db:
	TEST_MYSQL_DSN='$(TEST_MYSQL_DSN)' go test -count=1 ./...

up:
	docker compose up -d --build

down:
	docker compose down
