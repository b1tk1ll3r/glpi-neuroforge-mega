#!/bin/sh
# One-shot helper used by docker compose (as root, cap CHOWN/FOWNER/DAC_READ_SEARCH)
# to hand the bind-mounted data directories to the non-root runtime user.
# Read-only mounts (e.g. KB_DATA_MOUNT_MODE=ro) are left untouched; the runtime
# only needs read access there.
set -eu

uid=${KB_UID:-65532}
gid=${KB_GID:-65532}

is_readonly_mount() {
  awk -v m="$1" '$2 == m { print $4 }' /proc/mounts | tail -n 1 | grep -Eq '^ro(,|$)'
}

for dir in "${DATA_DIR:-/data/knowledge}" "${STAGING_DIR:-/data/staging}" "${BACKUP_DIR:-/data/backups}"; do
  [ -d "$dir" ] || continue
  if is_readonly_mount "$dir"; then
    echo "kb-data-init: $dir is mounted read-only, ownership unchanged"
    continue
  fi
  chown -R "$uid:$gid" "$dir"
  chmod u+rwx "$dir"
  echo "kb-data-init: $dir -> $uid:$gid"
done
