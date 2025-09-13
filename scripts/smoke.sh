#!/usr/bin/env bash
set -euo pipefail

# 소량 트랜잭션 연속 투입으로 파이프 정상 동작 점검
sleep 1

go run ./cmd/bench -n 50 -c 8 -ns bank