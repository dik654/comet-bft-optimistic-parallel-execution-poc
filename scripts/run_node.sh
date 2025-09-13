#!/usr/bin/env bash
set -euo pipefail

go run ./cmd/appd/main.go &
APP_PID=$!

echo "appd pid=$APP_PID"

cometbft start

kill $APP_PID || true