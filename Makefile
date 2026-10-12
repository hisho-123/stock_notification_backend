.PHONY: build test test-integration coverage vet fmt db-up db-down migrate

COMPOSE ?= $(shell if docker compose version >/dev/null 2>&1; then echo 'docker compose'; else echo 'docker-compose'; fi)

build:
	go build -trimpath -o bin/stock-notification ./cmd/stock-notification

test:
	go test -race ./...

test-integration:
	@test -n "$${TEST_DATABASE_URL}" || (echo 'TEST_DATABASE_URL is required' >&2; exit 1)
	go test -race -tags=integration ./tests/integration/...

coverage:
	python3 -m unittest discover -s scripts -p 'test_coverage.py'
	python3 scripts/coverage.py

vet:
	go vet ./...

fmt:
	gofmt -w cmd internal tests scripts

db-up:
	$(COMPOSE) -f deploy/compose.yaml up -d db

db-down:
	$(COMPOSE) -f deploy/compose.yaml down

# 初回スキーマ用。適用済みのDBでは再実行しない。
migrate:
	psql --dbname="$${DATABASE_URL:?DATABASE_URL is required}" -v ON_ERROR_STOP=1 -f migrations/000001_initial.up.sql
