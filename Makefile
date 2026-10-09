SHELL := /bin/bash
GO ?= go
COMPOSE ?= docker compose

.PHONY: help up down logs build run test race vet fmt coverage smoke infra clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-8s\033[0m %s\n", $$1, $$2}'

up: ## Start postgres + the api, then run the end-to-end smoke test
	$(COMPOSE) up -d --build
	@$(MAKE) --no-print-directory smoke

down: ## Stop the stack
	$(COMPOSE) down

logs: ## Tail the api log
	$(COMPOSE) logs -f api

build: ## Build the binary
	$(GO) build -o bin/gamelog-api ./cmd/server

run: ## Run locally (needs `make infra` for postgres)
	$(GO) run ./cmd/server

infra: ## Start only postgres
	$(COMPOSE) up -d postgres

test: ## Unit tests
	$(GO) test ./...

race: ## Unit tests with the race detector
	$(GO) test -race ./...

vet: ## Static analysis
	$(GO) vet ./...

fmt: ## Format
	$(GO) fmt ./...

lint: fmt vet ## Format + vet

coverage: ## Coverage report
	$(GO) test -coverprofile=coverage.out ./... && $(GO) tool cover -func=coverage.out | tail -1

smoke: ## End-to-end smoke test against a running api
	@PORT=$$(grep -E '^API_PORT=' .env 2>/dev/null | cut -d= -f2); \
	python3 scripts/smoke.py 127.0.0.1 $${PORT:-8080}

clean: ## Remove build output and volumes
	rm -rf bin coverage.out
	$(COMPOSE) down -v
