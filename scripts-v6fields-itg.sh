#!/bin/sh
# Integration tests on the private containers (ports 56471-56473). Usage: sh scripts-v6fields-itg.sh [-run Regexp]
export GOCACHE=${GOCACHE:-/tmp/v6f-out/gocache}
export INTEGRATION_DSN="postgres://postgres:pw@127.0.0.1:56471/torn?sslmode=disable"
export INTEGRATION_REDIS_URL="redis://127.0.0.1:56472/0"
export INTEGRATION_NATS_URL="nats://127.0.0.1:56473"
/snap/go/11067/bin/go test -tags=integration -p 1 -timeout 90m -v ./tests/ "$@" > /tmp/v6f-out/itg-full.txt 2>&1
echo "exit $?" >> /tmp/v6f-out/itg-full.txt
