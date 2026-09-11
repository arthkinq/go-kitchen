.PHONY: build test test-integration cover lint tidy docker-up docker-down run-kitchen run-partner

TEST_DATABASE_URL ?= postgres://postgres:postgres@localhost:5433/go_kitchen?sslmode=disable

RACE ?= $(if $(filter 1,$(shell go env CGO_ENABLED)),-race)

build:
	go build -o bin/kitchen-service ./cmd/kitchen-service
	go build -o bin/partner-restaurant ./cmd/partner-restaurant

test:
	go test $(RACE) ./...

test-integration:
	docker compose up -d postgres
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test $(RACE) -count=1 ./tests/integration/...

cover:
	docker compose up -d postgres
	TEST_DATABASE_URL="$(TEST_DATABASE_URL)" go test $(RACE) -count=1 -coverpkg=./internal/...,./cmd/... -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

docker-up:
	docker compose up --build -d

docker-down:
	docker compose down -v

run-kitchen:
	go run ./cmd/kitchen-service

run-partner:
	go run ./cmd/partner-restaurant
