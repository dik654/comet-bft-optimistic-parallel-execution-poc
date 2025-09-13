#!/usr/bin/env bash
set -euo pipefail

# === 0) cometbft 바이너리 경로 탐색/설치 ===
CBFT_BIN="${COMETBFT_BIN:-$(command -v cometbft || true)}"
if [ -z "${CBFT_BIN}" ]; then
  echo "cometbft not found. installing..."
  GO_BIN="$(go env GOPATH)/bin"
  go install github.com/cometbft/cometbft/cmd/cometbft@v0.38.17
  CBFT_BIN="${GO_BIN}/cometbft"
  if [ ! -x "${CBFT_BIN}" ]; then
    echo "failed to install cometbft to ${CBFT_BIN}"; exit 1
  fi
fi
echo "Using cometbft: ${CBFT_BIN}"
"${CBFT_BIN}" version || true

# === 1) 노드 초기화 ===
"${CBFT_BIN}" init

CFG="$HOME/.cometbft/config/config.toml"

# === 2) proxy_app 소켓 지정 ===
if grep -q '^proxy_app' "$CFG"; then
  sed -i.bak 's#^proxy_app = .*$#proxy_app = "tcp://127.0.0.1:26658"#' "$CFG" || true
fi

# === 3) 개발용 타임아웃 단축(옵션) ===
sed -i.bak 's/timeout_propose = ".*"/timeout_propose = "500ms"/' "$CFG" || true
sed -i.bak 's/timeout_commit  = ".*"/timeout_commit  = "500ms"/' "$CFG" || true

echo "✅ init done. Start appd then run scripts/run_node.sh"
