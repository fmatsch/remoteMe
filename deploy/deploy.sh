#!/usr/bin/env bash
# Installiert/aktualisiert den remoteMe-Relay-Server auf dem Ubuntu/Debian-Server hinter Caddy.
#   deploy/deploy.sh                       -> https://remote.depes.ch (eigene Adresse, DNS-A-Eintrag nötig)
#   PK_SITE=andere.domain deploy/deploy.sh
# Voraussetzung: SSH-Schlüssel ist am Server hinterlegt, Caddy läuft mit /etc/caddy/Caddyfile.
set -euo pipefail

HOST="${PK_SERVER:-root@138.201.56.28}"
KEY="${PK_KEY:-$HOME/.ssh/praxiskalender_ed25519}"
SITE="${PK_SITE:-remote.depes.ch}" # eigene Adresse: kein gemeinsamer Ursprung mit anderen Anwendungen
PORT="${RM_PORT:-4740}"
cd "$(dirname "$0")/.."

SSHO=(-i "$KEY" -o IdentitiesOnly=yes -o BatchMode=yes)
ssh_() { ssh "${SSHO[@]}" "$HOST" "$@"; }

echo "== Server bauen (linux/amd64)"
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/remoteme-server-linux-amd64 ./cmd/server

echo "== Hochladen"
ssh_ 'mkdir -p /opt/remoteme'
scp "${SSHO[@]}" -q dist/remoteme-server-linux-amd64 "$HOST:/opt/remoteme/remoteme-server.new"
sed "s#__PORT__#$PORT#" deploy/remoteme.service | ssh_ 'cat > /etc/systemd/system/remoteme.service'
scp "${SSHO[@]}" -q deploy/remote-install.sh "$HOST:/opt/remoteme/install.sh"

echo "== Installieren"
ssh_ "PORT=$PORT SITE='$SITE' bash /opt/remoteme/install.sh"
echo "Fertig."
