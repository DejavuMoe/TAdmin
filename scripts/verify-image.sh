#!/usr/bin/env bash
set -euo pipefail

image=${1:?image required}
version=${2:?expected version required}
platform=${PLATFORM:-linux/amd64}
volume=$(docker volume create)
work=$(mktemp -d)
container=
trap 'if [[ -n "$container" ]]; then docker rm "$container" >/dev/null; fi; docker volume rm "$volume" >/dev/null; rm -rf "$work"' EXIT
args=(--rm --network none --platform "$platform" --read-only --cap-drop ALL
  --security-opt no-new-privileges --pids-limit 256 --memory 256m
  -v "$volume:/data" -e TADMIN_BOT_TOKEN=123:TEST -e TADMIN_OWNER_ID=1)

test "$(docker image inspect --format '{{.Config.User}}' "$image")" = 65532:65532
test "$(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$image")" = "$platform"
[[ "$(docker run "${args[@]}" "$image" version)" == "TAdmin $version ("*" $platform)" ]]
docker run "${args[@]}" "$image" check
docker run "${args[@]}" "$image" backup /data/snapshot.db
container=$(docker create --platform "$platform" -v "$volume:/data" "$image" version)
docker cp "$container:/usr/share/licenses/tadmin/LICENSE" "$work/LICENSE"
docker cp "$container:/usr/share/licenses/tadmin/THIRD_PARTY_NOTICES.txt" "$work/THIRD_PARTY_NOTICES.txt"
cmp LICENSE "$work/LICENSE"
cmp THIRD_PARTY_NOTICES.txt "$work/THIRD_PARTY_NOTICES.txt"
docker cp "$container:/data/snapshot.db" "$work/snapshot.db"
python3 - "$work/snapshot.db" <<'PY'
import sqlite3
import sys
with sqlite3.connect(sys.argv[1]) as db:
    assert db.execute('PRAGMA integrity_check').fetchone() == ('ok',)
    assert db.execute('SELECT count(*) FROM schema_migrations').fetchone()[0] > 0
PY
if docker run "${args[@]}" "$image" backup /data/snapshot.db; then
  echo 'backup unexpectedly overwrote an existing snapshot' >&2
  exit 1
fi
# Exercise the documented restore procedure on this disposable volume only.
python3 - "$work/snapshot.db" <<'PY'
import sqlite3
import sys
with sqlite3.connect(sys.argv[1]) as db:
    db.execute("INSERT INTO meta(key,value) VALUES ('restore-proof','snapshot')")
PY
docker run --rm --network none --user 0 --volumes-from "$container" \
  -v "$work:/backup:ro" busybox:1.37 sh -eu -c '
    test -s /backup/snapshot.db
    mkdir /data/pre-restore
    for f in tadmin.db tadmin.db-wal tadmin.db-shm; do
      if test -f "/data/$f"; then mv "/data/$f" /data/pre-restore/; fi
    done
    cp /backup/snapshot.db /data/tadmin.db
    chown 65532:65532 /data/tadmin.db
    chmod 600 /data/tadmin.db
    test -s /data/pre-restore/tadmin.db
  '
docker run "${args[@]}" "$image" check
docker run "${args[@]}" "$image" backup /data/restored-check.db
docker cp "$container:/data/restored-check.db" "$work/restored-check.db"
python3 - "$work/restored-check.db" <<'PY'
import sqlite3
import sys
with sqlite3.connect(sys.argv[1]) as db:
    assert db.execute('PRAGMA integrity_check').fetchone() == ('ok',)
    assert db.execute("SELECT value FROM meta WHERE key='restore-proof'").fetchone() == ('snapshot',)
PY
echo "PASS: $platform $version; non-root, read-only root, offline check, backup and restore"
