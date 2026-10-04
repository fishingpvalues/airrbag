#!/bin/bash
# Run the integration suite against one real *Arr container.
#   scripts/integration.sh <app> <image> <port>
# Starts the *Arr, reads its generated API key, starts airrbag in front of
# it, runs `go test -tags integration ./tests/integration`, tears down.
set -euo pipefail

APP="$1"
IMAGE="$2"
PORT="$3"
NAME="airrbag-it-$APP"
PROXY_PORT=17999
WORK="$(mktemp -d)"

cleanup() {
    [ -n "${AIRRBAG_PID:-}" ] && kill "$AIRRBAG_PID" 2>/dev/null || true
    docker rm -f "$NAME" >/dev/null 2>&1 || true
    rm -rf "$WORK"
}
trap cleanup EXIT

docker run -d --name "$NAME" -e PUID=1000 -e PGID=1000 -p "127.0.0.1:$PORT:$PORT" "$IMAGE" >/dev/null

echo "waiting for $APP on :$PORT"
for i in $(seq 1 90); do
    if curl -sf "http://127.0.0.1:$PORT/ping" >/dev/null; then break; fi
    [ "$i" -eq 90 ] && { docker logs "$NAME" | tail -50; echo "$APP never came up"; exit 1; }
    sleep 2
done

KEY=""
for i in $(seq 1 30); do
    KEY=$(docker exec "$NAME" sh -c 'cat /config/config.xml 2>/dev/null' | sed -n 's:.*<ApiKey>\(.*\)</ApiKey>.*:\1:p')
    [ -n "$KEY" ] && break
    sleep 1
done
[ -n "$KEY" ] || { echo "no API key in config.xml"; exit 1; }

cat > "$WORK/airrbag.yml" <<YAML
instances:
  - name: $APP
    listen: "127.0.0.1:$PROXY_PORT"
    upstream: http://127.0.0.1:$PORT
    api_key: \${IT_ARR_KEY}
log_level: debug
YAML

go build -o "$WORK/airrbag" ./cmd/airrbag
IT_ARR_KEY="$KEY" "$WORK/airrbag" serve -config "$WORK/airrbag.yml" > "$WORK/airrbag.log" 2>&1 &
AIRRBAG_PID=$!

for i in $(seq 1 60); do
    if curl -sf "http://127.0.0.1:$PROXY_PORT/__airrbag/health" >/dev/null; then break; fi
    [ "$i" -eq 60 ] && { cat "$WORK/airrbag.log"; echo "airrbag never became ready"; exit 1; }
    sleep 1
done

IT_APP="$APP" IT_ARR_URL="http://127.0.0.1:$PORT" IT_ARR_KEY="$KEY" \
IT_PROXY_URL="http://127.0.0.1:$PROXY_PORT" \
    go test -tags integration ./tests/integration -v -count=1 || { cat "$WORK/airrbag.log"; exit 1; }
