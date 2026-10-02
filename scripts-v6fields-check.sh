#!/bin/sh
# Runs the Go checks of feat/v6-server-fields with a private build cache and
# writes the results next to this script's out dir. Containers: see run-itg below.
set -u
OUT=${OUT:-/tmp/v6f-out}
mkdir -p "$OUT"
export GOCACHE=${GOCACHE:-$OUT/gocache}
go build ./... >"$OUT/build.txt" 2>&1; echo "build exit $?" >>"$OUT/build.txt"
go vet ./... >"$OUT/vet.txt" 2>&1; echo "vet exit $?" >>"$OUT/vet.txt"
go vet -tags=integration ./tests/ >"$OUT/vet-itg.txt" 2>&1; echo "vet-itg exit $?" >>"$OUT/vet-itg.txt"
go test ./... 2>&1 | grep -v "no test files" >"$OUT/test.txt"; echo "test done" >>"$OUT/test.txt"
INTEGRATION_DSN="postgres://postgres:pw@127.0.0.1:56471/torn?sslmode=disable" \
INTEGRATION_REDIS_URL="redis://127.0.0.1:56472/0" \
INTEGRATION_NATS_URL="nats://127.0.0.1:56473" \
go test -tags=integration -p 1 ./tests/ >"$OUT/itg.txt" 2>&1; echo "itg exit $?" >>"$OUT/itg.txt"
