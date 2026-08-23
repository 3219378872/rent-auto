SHELL := /bin/bash   # test 目标的 /dev/tcp 端口探测是 bash 特性，dash 下会静默退化为纯单测

.PHONY: gate fmt fmt-check lint vet build test test-integration cover cover-gate \
	frontend-install frontend-typecheck frontend-lint frontend-test frontend-build \
	dev-up dev-down server web migrate-up migrate-down migrate-check migrate-new \
	worktree-new release clean

BACKEND := backend
FRONTEND := frontend

PG_HOST_PORT ?= 15432
COV_MIN ?= 70

HAS_BACKEND := $(shell test -f $(BACKEND)/go.mod && echo yes || echo no)
HAS_FRONTEND := $(shell test -f $(FRONTEND)/package.json && echo yes || echo no)

ifeq ($(HAS_BACKEND),yes)
gate: fmt-check lint vet build test cover-gate migrate-check
else
gate:
endif
	@if [ "$(HAS_FRONTEND)" = "yes" ]; then $(MAKE) frontend-typecheck frontend-lint frontend-test frontend-build; fi
	@echo "=== GATE PASSED ==="

fmt:
	cd $(BACKEND) && gofmt -w ./cmd ./internal

fmt-check:
	@test -z "$$(gofmt -l $(BACKEND)/cmd $(BACKEND)/internal 2>/dev/null)" || { gofmt -l $(BACKEND)/cmd $(BACKEND)/internal; exit 1; }

lint:
	cd $(BACKEND) && golangci-lint run ./...

vet:
	cd $(BACKEND) && go vet ./...

build:
	cd $(BACKEND) && go build ./...

test:
	cd $(BACKEND) && if [ -n "$$TEST_DATABASE_URL" ] || (echo > /dev/tcp/localhost/$(PG_HOST_PORT)) 2>/dev/null; then \
		echo ">> unit + integration tests"; \
		TEST_DATABASE_URL=$${TEST_DATABASE_URL:-postgres://rentauto:rentauto@localhost:$(PG_HOST_PORT)/rentauto?sslmode=disable} \
			go test -tags=integration -p 1 ./... -race -count=1 -coverprofile=coverage.out -coverpkg=./internal/...; \
	else \
		echo ">> unit tests only (no database detected)"; \
		go test ./... -race -count=1 -coverprofile=coverage.out -coverpkg=./internal/...; \
	fi
	cd $(BACKEND) && go tool cover -func=coverage.out | tail -1

test-integration:
	cd $(BACKEND) && go test -tags=integration ./... -count=1

cover:
	cd $(BACKEND) && go tool cover -html=coverage.out -o coverage.html

# cover-gate enforces the AGENTS.md quantified gate: per-package statement
# coverage of every pure-logic domain must reach COV_MIN percent.
cover-gate:
	@$(BACKEND)/../scripts/coverage-gate.sh $(BACKEND) $(COV_MIN)

# ---- frontend ----
frontend-install:
	cd $(FRONTEND) && pnpm install --frozen-lockfile

frontend-typecheck:
	cd $(FRONTEND) && pnpm exec tsc --noEmit

frontend-lint:
	cd $(FRONTEND) && pnpm exec eslint . --max-warnings=0

frontend-test:
	cd $(FRONTEND) && pnpm exec vitest run

frontend-build:
	cd $(FRONTEND) && pnpm exec vite build

# ---- dev infra ----
dev-up:
	docker compose -f deploy/docker-compose.yml up -d postgres

dev-down:
	docker compose -f deploy/docker-compose.yml down

server:
	cd $(BACKEND) && go run ./cmd/server

web:
	cd $(FRONTEND) && pnpm dev

# ---- migrations (embedded CLI) ----
migrate-up:
	cd $(BACKEND) && go run ./cmd/migrate up

migrate-down:
	cd $(BACKEND) && go run ./cmd/migrate down 1

migrate-check:
	@if [ "$(HAS_BACKEND)" = "yes" ]; then cd $(BACKEND) && go test ./internal/store -run TestMigrationsUpDown -count=1; fi

migrate-new:
	@test -n "$(NAME)" || { echo "usage: make migrate-new NAME=add_xxx"; exit 1; }
	@last=$$(ls $(BACKEND)/migrations 2>/dev/null | grep -E '^[0-9]{4}_.*\.up\.sql$$' | sort | tail -1 | cut -d_ -f1); \
	if [ -n "$$last" ]; then seqn=$$(expr $$last + 1); else seqn=1; fi; \
	ver=$$(printf '%04d' $$seqn); \
	{ echo '-- +migrate Up'; echo; } > $(BACKEND)/migrations/$${ver}_$(NAME).up.sql; \
	{ echo '-- +migrate Down'; echo; } > $(BACKEND)/migrations/$${ver}_$(NAME).down.sql; \
	echo "created $(BACKEND)/migrations/$${ver}_$(NAME).{up,down}.sql"

# ---- git workflow helpers ----
worktree-new:
	@test -n "$(NAME)" || { echo "usage: make worktree-new NAME=feat-xxx"; exit 1; }
	git worktree add ../rent-auto-wt/$(NAME) -b feat/$(NAME)
	@echo "worktree ready: ../rent-auto-wt/$(NAME) (branch feat/$(NAME))"

release: gate
	cd $(BACKEND) && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o bin/rent-auto-server ./cmd/server
	cd $(FRONTEND) && pnpm exec vite build
	@echo "artifacts: backend/bin/rent-auto-server, frontend/dist"

clean:
	rm -f $(BACKEND)/coverage.out $(BACKEND)/coverage.html
	rm -rf $(FRONTEND)/dist
