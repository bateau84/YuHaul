#!/bin/sh
# Exercise the built image's unchanged entrypoint/CMD, not a substitute server.
set -eu

runtime=${CONTAINER_RUNTIME:-docker}
image=${1:?usage: sh scripts/smoke-image.sh IMAGE}
name="yuhaul-smoke-$$"
volume="$name-data"

cleanup() {
    "$runtime" rm -fv "$name" >/dev/null 2>&1 || :
    "$runtime" volume rm "$volume" >/dev/null 2>&1 || :
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

check_startup() {
    attempts=0
    until "$runtime" exec "$name" wget -q -O /dev/null http://127.0.0.1:8080/api/overview; do
        attempts=$((attempts + 1))
        if [ "$attempts" -ge 30 ] || [ "$("$runtime" inspect --format '{{.State.Running}}' "$name")" != true ]; then
            "$runtime" logs "$name" >&2
            echo 'FAIL: default image entrypoint did not become ready' >&2
            exit 1
        fi
        sleep 1
    done
    "$runtime" exec "$name" sh -ec '
        test "$(id -u)" = 1000
        test -s /data/yuhaul.db
        test "$(stat -c %u /data/yuhaul.db)" = 1000
        wget -q -O - http://127.0.0.1:8080/ | grep -q "<html"
    '
}

# The image-declared anonymous volume must inherit writable /data ownership.
"$runtime" run -d --name "$name" "$image" >/dev/null
check_startup
"$runtime" rm -fv "$name" >/dev/null
echo 'PASS: default entrypoint, anonymous volume, UID 1000, SQLite, API and SPA'

# A genuinely fresh named volume must work, and the database must reopen.
"$runtime" volume create "$volume" >/dev/null
"$runtime" run -d --name "$name" -v "$volume:/data" "$image" >/dev/null
check_startup
"$runtime" restart "$name" >/dev/null
check_startup
echo 'PASS: default entrypoint, fresh named volume and database restart'
