.PHONY: gate fmt fmt-check lint vet build test test-integration cover \
	frontend-install frontend-typecheck frontend-lint frontend-test frontend-build \
	dev-up dev-down server web migrate-up migrate-down migrate-check migrate-new \
	worktree-new release clean

BACKEND := backend
FRONTEND := frontend

HAS_BACKEND := $(shell test -f $(BACKEND)/go.mod && echo yes || echo no)
HAS_FRONTEND := $(shell test -f $(FRONTEND)/package.json && echo yes || echo no)

ifeq ($(HAS_BACKEND),yes)
gate: fmt-check lint vet build test migrate-check
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
	cd $(BACKEND) && go test ./... -race -count=1 -coverprofile=coverage.out -coverpkg=./internal/... . ./internal/... && go tool cover -func=coverage.out | tail -1

test-integration:
	cd $(BACKEND) && go test -tags=integration ./... -count=1

cover:
	cd $(BACKEND) && go tool cover -html=coverage.out -o coverage.html

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
	@if [ "$(HAS_BACKEND)" = "yes" ]; then cd $(BACKEND) && TEST_MIGRATE_CHECK=1 go test ./internal/store -run TestMigrationsUpDown -count=1; fi

migrate-new:
	@test -n "$(NAME)" || { echo "usage: make migrate-new NAME=add_xxx"; exit 1; }
	@ts=$$(date +%Y%m%d%H%M%S); \
	printf '-- +migrate Up\n\n' > $(BACKEND)/migrations/$${ts}_$(NAME).up.sql; \
	printf '-- +migrate Down\n\n' > $(BACKEND)/migrations/$${ts}_$(NAME).down.sql; \
	echo "created $(BACKEND)/migrations/$${ts}_$(NAME).{up,down}.sql"

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
