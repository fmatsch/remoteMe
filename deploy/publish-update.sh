#!/usr/bin/env bash
# Baut die aktuelle Version, signiert sie mit deinem Update-Schlüssel und stellt sie auf dem
# Relay-Server bereit. Danach erscheint bei jedem älteren Computer "Auf X aktualisieren".
#   deploy/publish-update.sh
# Voraussetzung: Server mit deploy/deploy.sh auf demselben Stand; Schlüssel liegt (nur hier) unter
# "~/Library/Application Support/remoteMe-signing/update.key" – sichern! Ohne ihn können
# installierte Programme nicht mehr aktualisiert werden.
set -euo pipefail

HOST="${PK_SERVER:-root@138.201.56.28}"
KEY="${PK_KEY:-$HOME/.ssh/praxiskalender_ed25519}"
SIGNKEY="${RM_SIGNKEY:-$HOME/Library/Application Support/remoteMe-signing/update.key}"
cd "$(dirname "$0")/.."
SSHO=(-i "$KEY" -o IdentitiesOnly=yes -o BatchMode=yes)

[ -f "$SIGNKEY" ] || { echo "Update-Schlüssel fehlt: $SIGNKEY"; exit 1; }
VERSION="$(grep -m1 '^var version' cmd/agent/agent.go | cut -d'"' -f2)"
grep -q "\"$(go run ./cmd/signupdate public "$SIGNKEY")\"" cmd/agent/updatekey.go || { echo "Schlüssel passt nicht zu cmd/agent/updatekey.go"; exit 1; }
[ -z "$(git status --porcelain)" ] || { echo "Bitte zuerst committen (nur geprüfte Stände verteilen)."; exit 1; }

OUT="$(mktemp -d)"; trap 'rm -rf "$OUT"' EXIT
echo "== macOS (in einer Kopie, die laufende App bleibt unberührt)"
git clone -q . "$OUT/src"
(cd "$OUT/src" && bash ./build-macos.sh >"$OUT/mac.log" 2>&1) || { tail -20 "$OUT/mac.log"; exit 1; }
mkdir "$OUT/pkg"
ditto -c -k --keepParent "$OUT/src/dist/macos/remoteMe.app" "$OUT/pkg/remoteMe-$VERSION-darwin.zip"
echo "== Windows und Linux"
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w -H=windowsgui" -o "$OUT/pkg/remoteMe-$VERSION-windows-amd64.exe" ./cmd/agent
for arch in amd64 arm64; do
  CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags "-s -w" -o "$OUT/pkg/remoteMe-$VERSION-linux-$arch" ./cmd/agent
done

echo "== Signieren"
go run ./cmd/signupdate sign "$SIGNKEY" "$VERSION" "$OUT/pkg" \
  "darwin-universal=remoteMe-$VERSION-darwin.zip" "windows-amd64=remoteMe-$VERSION-windows-amd64.exe" \
  "linux-amd64=remoteMe-$VERSION-linux-amd64" "linux-arm64=remoteMe-$VERSION-linux-arm64"

echo "== Hochladen"
# Erst die Pakete, zuletzt das Manifest; alte Pakete werden danach entfernt.
ssh "${SSHO[@]}" "$HOST" 'rm -rf /opt/remoteme/updates.new && mkdir -p /opt/remoteme/updates.new'
scp "${SSHO[@]}" -q "$OUT/pkg/"* "$HOST:/opt/remoteme/updates.new/"
ssh "${SSHO[@]}" "$HOST" 'chown -R root:root /opt/remoteme/updates.new && chmod 755 /opt/remoteme/updates.new && chmod 644 /opt/remoteme/updates.new/* && rm -rf /opt/remoteme/updates.old && { mv /opt/remoteme/updates /opt/remoteme/updates.old 2>/dev/null || true; } && mv /opt/remoteme/updates.new /opt/remoteme/updates && rm -rf /opt/remoteme/updates.old'
echo "Bereitgestellt: $VERSION"
