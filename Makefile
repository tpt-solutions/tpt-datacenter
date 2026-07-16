# SPDX-FileCopyrightText: 2024 TPT Solutions
# SPDX-License-Identifier: MIT OR Apache-2.0
#
# Convenience targets for local development in Simulator mode.
# The full stack is documented in docs/getting-started.md.

TOKEN ?= devtoken

.PHONY: build
build: ## Build the whole workspace (Rust + Go)
	cargo build --workspace
	cd api && go build ./... && cd ..
	cd dashboard && go build ./... && cd ..
	cd go-telemetry && go build ./... && cd ..

.PHONY: test
test: ## Run all tests (Rust + Go)
	cargo test --workspace
	cd api && go test ./... && cd ..
	cd go-telemetry && go test ./... && cd ..

.PHONY: questdb
questdb: ## Start QuestDB via docker compose
	docker compose -f deploy/questdb/docker-compose.dev.yml up -d

.PHONY: demo
demo: ## Bring up the full simulator stack (compose: edge, services, dashboard)
	docker compose up --build
	@echo "Dashboard: http://localhost:8085  (API token: $(TOKEN))"

.PHONY: demo-down
demo-down: ## Tear down the demo stack
	docker compose down

.PHONY: demo-local
demo-local: build questdb ## Run the full stack locally (no Docker) on loopback
	cd go-telemetry && go run ./cmd/api -addr :8080 -questdb http://localhost:9000 & \
	go run ./cmd/topology -spec ../deploy/topology/facility.json -addr :8081 & \
	cd ../api && go run ./cmd/control -addr :8082 -spec ../deploy/topology/facility.json -token $(TOKEN) & \
	go run ./cmd/hardware -addr :8083 -token $(TOKEN) & \
	go run ./cmd/orchestrator -addr :8084 -token $(TOKEN) & \
	cd ../rust-edge && cargo run --bin tpt-edge & \
	cd ../dashboard && go run ./cmd/dashboard -addr :8085 & \
	wait

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  %-14s %s\n", $$1, $$2}'
