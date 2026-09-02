#!/bin/sh
set -eu

certificate=/etc/nginx/tls/tls.crt
private_key=/etc/nginx/tls/tls.key

: "${TLS_CERT_PEM:?TLS_CERT_PEM is required}"
: "${TLS_KEY_PEM:?TLS_KEY_PEM is required}"

mkdir -p /etc/nginx/tls
umask 077
printf '%b\n' "$TLS_CERT_PEM" > "$certificate"
printf '%b\n' "$TLS_KEY_PEM" > "$private_key"
chmod 600 "$private_key"
