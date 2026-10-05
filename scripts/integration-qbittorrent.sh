#!/bin/bash
# Run the qBittorrent client tests against one real qBittorrent release.
#   scripts/integration-qbittorrent.sh <image> [legacy|sqlite]
# Starts qBittorrent with a payload folder mounted at /downloads, finds the
# WebUI password (adminadmin before 4.6.1, a temporary one in the log since),
# mounts /config so the test can also read BT_backup with the resume reader,
# and runs `go test -tags integration_qbittorrent ./tests/integration`.
set -euo pipefail

IMAGE="$1"
STORAGE="${2:-legacy}"
NAME="airrbag-it-qbt"
PORT=18080
WORK="$(mktemp -d)"
CONF="$(mktemp -d)"
chmod 777 "$WORK" "$CONF"

cleanup() {
    docker rm -f "$NAME" >/dev/null 2>&1 || true
    rm -rf "$WORK"
    docker run --rm -v "$CONF:/c" alpine:3.22 sh -c 'rm -rf /c/*' >/dev/null 2>&1 || true
    rm -rf "$CONF" 2>/dev/null || true
}
trap cleanup EXIT

# qBittorrent stores resume data in BT_backup unless told otherwise; the
# sqlite runs set Session\ResumeDataStorageType before the first start.
if [ "$STORAGE" = "sqlite" ]; then
    mkdir -p "$CONF/qBittorrent"
    printf '[BitTorrent]\nSession\\ResumeDataStorageType=SQLite\n' >"$CONF/qBittorrent/qBittorrent.conf"
    chmod -R 777 "$CONF/qBittorrent"
fi

docker run -d --name "$NAME" -e PUID="$(id -u)" -e PGID="$(id -g)" -e WEBUI_PORT="$PORT" \
    -v "$WORK:/downloads" -v "$CONF:/config" -p "127.0.0.1:$PORT:$PORT" "$IMAGE" >/dev/null

echo "waiting for qBittorrent on :$PORT"
for i in $(seq 1 90); do
    if curl -s -o /dev/null "http://127.0.0.1:$PORT/"; then break; fi
    [ "$i" -eq 90 ] && { docker logs "$NAME" | tail -50; echo "qBittorrent never came up"; exit 1; }
    sleep 2
done

PASS="adminadmin"
for i in $(seq 1 15); do
    TEMP=$(docker logs "$NAME" 2>&1 | sed -n 's/.*temporary password is provided for this session: \([^ ]*\).*/\1/p' | tail -1)
    [ -n "$TEMP" ] && { PASS="$TEMP"; break; }
    sleep 1
done

IT_QB_URL="http://127.0.0.1:$PORT" IT_QB_USER=admin IT_QB_PASS="$PASS" IT_QB_DIR="$WORK" IT_QB_RESUME="$CONF/qBittorrent/BT_backup" IT_QB_CONF="$CONF" IT_QB_STORAGE="$STORAGE" IT_QB_IMAGE="$IMAGE" \
    go test -tags integration_qbittorrent ./tests/integration -run "TestQBittorrent" -v -count=1 || { docker logs "$NAME" | tail -40; exit 1; }
