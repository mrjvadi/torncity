.PHONY: build vet test test-integration tidy up down migrate lint

build:   ; go build ./...
vet:     ; go vet ./...
test:    ; go test ./...

# Integration tests are behind a build tag AND skip unless the services they
# need are configured, so `make test` above stays fast and offline.
# Export INTEGRATION_DSN, INTEGRATION_REDIS_URL and INTEGRATION_NATS_URL first.
test-integration: ; go test -tags=integration ./tests/ -v
tidy:    ; go mod tidy
lint:    ; gofmt -l . | tee /dev/stderr | test -z "$$(cat)"
up:      ; docker compose up -d
down:    ; docker compose down -v
migrate: ; go run ./cmd/admin migrate
