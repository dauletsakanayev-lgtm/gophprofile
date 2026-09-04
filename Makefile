BIN_DIR := bin

.PHONY: build test cover lint fmt db-up db-down

build:
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/server ./cmd/server
	go build -o $(BIN_DIR)/worker ./cmd/worker

test:
	go test ./... -count=1

cover:
	go test ./... -count=1 -coverprofile=coverage.out
	go tool cover -func=coverage.out | tail -1

lint:
	go vet ./...

fmt:
	gofmt -s -w .

db-up:
	docker compose up -d

db-down:
	docker compose down