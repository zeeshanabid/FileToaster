#!/bin/sh
set -e

# Default to 65532 (the standard nonroot UID:GID) if PUID/PGID not overridden
PUID="${PUID:-65532}"
PGID="${PGID:-65532}"
USER_NAME="filetoaster"

# Ensure the group exists for our GID (create if missing)
if ! getent group "$PGID" > /dev/null 2>&1; then
    addgroup -g "$PGID" -S "$USER_NAME" 2>/dev/null || true
fi
GROUP_NAME="$(getent group "$PGID" | cut -d: -f1)"

# Ensure the user exists for our UID (create if missing)
if ! getent passwd "$PUID" > /dev/null 2>&1; then
    adduser -u "$PUID" -G "$GROUP_NAME" -D -H -s /sbin/nologin "$USER_NAME" 2>/dev/null || true
fi

# Fix permissions on the config dir so the user can write
mkdir -p "/home/$USER_NAME/.filetoaster"
chown -R "$PUID:$PGID" "/home/$USER_NAME/.filetoaster"

# Drop privileges and exec the binary
exec su-exec "$PUID:$PGID" /app/filetoaster
