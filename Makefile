.PHONY: run test test-integration vet

run:
	go run ./cmd/server

test:
	go test ./...

test-integration:
	TEST_DATABASE_URL=$${TEST_DATABASE_URL:-postgres://postgres:postgres@localhost:5432/leafygo_test?sslmode=disable} go test -p 1 ./...

vet:
	go vet ./...
