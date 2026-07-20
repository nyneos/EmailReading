#!/usr/bin/env bash
# Smoke-test LOCAL / API / SFTP destinations via POST /v1/storage/put
# (no email / transform tool needed).
#
# Prerequisites:
#   - Email service running on :8182
# For SFTP: start Go test server first — go run ./scripts/sftp_test_server/
#
# Usage:
#   cd CIMPLR-Email-Service
#   export EMAIL_SERVICE_KEY=cimplr-email-dev-key-change-in-prod
#   ./scripts/test_storage_destinations.sh [local|api|sftp|all]

set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

BASE_URL="${EMAIL_SERVICE_URL:-http://localhost:8182}"
KEY="${EMAIL_SERVICE_KEY:-cimplr-email-dev-key-change-in-prod}"
MODE="${1:-all}"

# tiny payload: "hello-transform"
CONTENT_B64="$(printf 'hello-transform' | base64 | tr -d '\n')"

put() {
  local dest="$1"
  shift
  echo ""
  echo "=== Testing destination=$dest ==="
  curl -sS -X POST "$BASE_URL/v1/storage/put" \
    -H "Authorization: Bearer $KEY" \
    -H "Content-Type: application/json" \
    -d "$@" | python3 -m json.tool 2>/dev/null || cat
  echo ""
}

test_local() {
  put LOCAL "$(cat <<EOF
{
  "content_base64": "$CONTENT_B64",
  "content_type": "application/json",
  "file_ext": ".json",
  "destination_type": "LOCAL",
  "output_name_prefix": "FDrates",
  "append_datetime": true,
  "local_folder": "cash/statements"
}
EOF
)"
  echo "Check: ls -la transformed/cash/statements/"
  ls -la transformed/cash/statements/ 2>/dev/null || ls -la ./transformed/cash/statements/ 2>/dev/null || true
}

test_api() {
  put API "$(cat <<EOF
{
  "content_base64": "$CONTENT_B64",
  "content_type": "application/json",
  "file_ext": ".json",
  "destination_type": "API",
  "output_name_prefix": "FDrates",
  "append_datetime": true,
  "api_url": "$BASE_URL/v1/storage/test-receive",
  "api_auth_token": "$KEY"
}
EOF
)"
  echo "Check: ls -la transformed/api-inbox/"
  ls -la transformed/api-inbox/ 2>/dev/null || ls -la ./transformed/api-inbox/ 2>/dev/null || true

  put API "$(cat <<EOF
{
  "content_base64": "$CONTENT_B64",
  "content_type": "application/json",
  "file_ext": ".json",
  "destination_type": "API",
  "output_name_prefix": "FDrates2",
  "append_datetime": true,
  "api_url": "$BASE_URL/v1/storage/test-receive-2",
  "api_auth_token": "$KEY"
}
EOF
)"
  echo "Check: ls -la transformed/api-inbox-2/"
  ls -la transformed/api-inbox-2/ 2>/dev/null || ls -la ./transformed/api-inbox-2/ 2>/dev/null || true
}

test_sftp() {
  put SFTP "$(cat <<EOF
{
  "content_base64": "$CONTENT_B64",
  "content_type": "application/json",
  "file_ext": ".json",
  "destination_type": "SFTP",
  "output_name_prefix": "FDrates",
  "append_datetime": true,
  "sftp_host": "127.0.0.1",
  "sftp_port": 2222,
  "sftp_user": "cimplr",
  "sftp_password": "cimplr123",
  "sftp_folder": "upload"
}
EOF
)"
  echo "Check: ls -la sftp-data/upload/  (or sftp-data/)"
  ls -la sftp-data/upload/ 2>/dev/null || ls -la sftp-data/ 2>/dev/null || true
}

case "$MODE" in
  local) test_local ;;
  api)   test_api ;;
  sftp)  test_sftp ;;
  all)
    test_local
    test_api
    test_sftp
    ;;
  *)
    echo "Usage: $0 [local|api|sftp|all]"
    exit 1
    ;;
esac
