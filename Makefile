.PHONY: build vet test tidy up down migrate lint

build:   ; go build ./...
vet:     ; go vet ./...
test:    ; go test ./...
tidy:    ; go mod tidy
lint:    ; gofmt -l . | tee /dev/stderr | test -z "$$(cat)"
up:      ; docker compose up -d
down:    ; docker compose down -v
migrate: ; go run ./cmd/admin migrate
