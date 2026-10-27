#!/bin/sh
set -eu

current_uid="$(id -u)"
current_gid="$(id -g)"
if ! getent passwd "$current_uid" >/dev/null; then
  umask 077
  passwd_file="$(mktemp /tmp/desktopctl-passwd.XXXXXX)"
  group_file="$(mktemp /tmp/desktopctl-group.XXXXXX)"
  cp /etc/passwd "$passwd_file"
  cp /etc/group "$group_file"
  printf 'desktopctl:x:%s:%s:Desktopctl operator:/home/operator:/bin/sh\n' "$current_uid" "$current_gid" >> "$passwd_file"
  if ! getent group "$current_gid" >/dev/null; then
    printf 'desktopctl:x:%s:\n' "$current_gid" >> "$group_file"
  fi
  export NSS_WRAPPER_PASSWD="$passwd_file"
  export NSS_WRAPPER_GROUP="$group_file"
  export LD_PRELOAD=/usr/local/lib/libnss_wrapper.so
fi

if [ "${1:-}" = "--check-ssh-identity" ]; then
  ssh -G localhost >/dev/null
  exit 0
fi

exec /app/control-plane "$@"
