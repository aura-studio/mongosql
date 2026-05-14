#!/usr/bin/env bash
set -euo pipefail

# ─── 可修改参数 ───
LISTEN="${LISTEN:-127.0.0.1:3306}"
MONGO_URI="${MONGO_URI:-mongodb://localhost:27017}"
DB="${DB:-sqlmongo}"
USER="${USER:-root}"
PASSWORD="${PASSWORD:-}"

cd "$(dirname "$0")/.."

echo "starting mysql-simulator (listen=$LISTEN db=$DB) ..."
exec go run ./mysql-simulator \
    --listen   "$LISTEN" \
    --mongo-uri "$MONGO_URI" \
    --db        "$DB" \
    --user      "$USER" \
    --password  "$PASSWORD"
