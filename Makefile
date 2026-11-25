.PHONY: run seed test test-integration test-all vet web-install web-dev web-build web-test obs-up obs-down obs-config

run:
	go run ./cmd/server

seed:
	go run ./cmd/seed

test:
	go test ./...

test-integration:
	TEST_DATABASE_URL=$${TEST_DATABASE_URL:-postgres://postgres:postgres@localhost:5432/leafygo_test?sslmode=disable} \
	TEST_TARGET_DATABASE_URL=$${TEST_TARGET_DATABASE_URL:-postgres://postgres:postgres@localhost:5432/leafygo_target_test?sslmode=disable} \
	go test -race -count=1 -p 1 ./...

vet:
	go vet ./...

# --- SPA -------------------------------------------------------------------

web-install:
	cd web && npm ci

web-dev:
	cd web && npm run dev

web-build:
	cd web && npm run build

web-test:
	cd web && npm test

test-all: test web-test

# --- observability (bonus) -------------------------------------------------

obs-up:
	docker compose -f deploy/observability/docker-compose.observability.yml up -d

obs-down:
	docker compose -f deploy/observability/docker-compose.observability.yml down

obs-config:
	docker compose -f deploy/observability/docker-compose.observability.yml config
