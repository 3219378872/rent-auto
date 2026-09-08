#!/bin/sh
set -eu
umask 077

directory=/backups/rent-auto
interval=${BACKUP_INTERVAL_SECONDS:-86400}
retry=${BACKUP_RETRY_SECONDS:-300}
once=${BACKUP_ONCE:-false}
temporary=
active_pid=

log() {
    printf '%s %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"
}

for value in "$interval" "$retry"; do
    case "$value" in ''|*[!0-9]*) log "backup interval must be a positive integer" >&2; exit 2 ;; esac
done
if [ "$interval" -lt 60 ] || [ "$interval" -gt 86400 ] || [ "$retry" -lt 1 ] || [ "$retry" -gt 3600 ]; then
    log "backup interval must be 60..86400 seconds and retry 1..3600 seconds" >&2
    exit 2
fi
case "$once" in true|false) ;; *) log "BACKUP_ONCE must be true or false" >&2; exit 2 ;; esac
if [ -L "$directory" ]; then
    log "backup directory must not be a symlink" >&2
    exit 2
fi
mkdir -p "$directory"
chmod 0700 "$directory"

cleanup() {
    if [ -n "$active_pid" ]; then
        kill "$active_pid" 2>/dev/null || true
        wait "$active_pid" 2>/dev/null || true
    fi
    if [ -n "$temporary" ]; then
        rm -f -- "$temporary"
    fi
}
trap cleanup EXIT
trap 'exit 143' TERM
trap 'exit 130' INT

backup_once() {
    if ! temporary=$(mktemp "$directory/.rent-auto-$(date -u +%Y%m%dT%H%M%SZ)-XXXXXXXX.partial"); then
        log "cannot create temporary backup" >&2
        return 1
    fi
    pg_dump --format=custom --no-owner --no-acl --file="$temporary" &
    active_pid=$!
    if wait "$active_pid"; then
        active_pid=
    else
        active_pid=
        rm -f -- "$temporary"
        temporary=
        return 1
    fi
    if [ ! -s "$temporary" ] || ! pg_restore --list "$temporary" >/dev/null; then
        log "dump validation failed; no backup published" >&2
        rm -f -- "$temporary"
        temporary=
        return 1
    fi
    filename=${temporary##*/}
    destination="$directory/${filename#.}"
    destination=${destination%.partial}.dump
    if [ -e "$destination" ]; then
        log "backup destination already exists; refusing overwrite" >&2
        rm -f -- "$temporary"
        temporary=
        return 1
    fi
    if ! mv -- "$temporary" "$destination"; then
        log "cannot publish completed backup" >&2
        rm -f -- "$temporary"
        temporary=
        return 1
    fi
    temporary=
    # Only finalized dumps created by this script are retention candidates.
    # Failed dumps never trigger deletion of existing recovery points.
    if ! find "$directory" -mindepth 1 -maxdepth 1 -type f \
        -name 'rent-auto-????????T??????Z-????????.dump' -mtime +13 -delete; then
        log "backup published but retention cleanup failed" >&2
        return 1
    fi
    log "backup published: ${destination##*/}; retention 14 days"
}

while :; do
    if backup_once; then
        if [ "$once" = true ]; then exit 0; fi
        delay=$interval
    else
        log "backup failed; existing backups retained" >&2
        if [ "$once" = true ]; then exit 1; fi
        delay=$retry
    fi
    sleep "$delay" &
    active_pid=$!
    wait "$active_pid"
    active_pid=
done
