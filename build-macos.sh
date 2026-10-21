#!/usr/bin/env bash
# Baut remoteMe.app (Universal: Apple Silicon + Intel) und optional ein DMG.
#   ./build-macos.sh [-s https://server] [-v 1.1.0] [-i "Developer ID Application: …"] [--dmg]
# Ohne -i wird ad hoc signiert (läuft auf diesem Mac; auf anderen Macs: Rechtsklick > Öffnen).
set -euo pipefail
cd "$(dirname "$0")"

SERVER=""; VERSION="$(grep -m1 'const version' cmd/agent/agent.go | cut -d'"' -f2)"; IDENT="-"; DMG=0
while [ $# -gt 0 ]; do
  case "$1" in
    -s) SERVER="$2"; shift 2 ;;
    -v) VERSION="$2"; shift 2 ;;
    -i) IDENT="$2"; shift 2 ;;
    --dmg) DMG=1; shift ;;
    *) echo "Unbekannte Option: $1"; exit 1 ;;
  esac
done

OUT=dist/macos
APP="$OUT/remoteMe.app"
rm -rf "$OUT"; mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources" "$OUT/tmp"

LD="-s -w"
[ -n "$SERVER" ] && LD="$LD -X main.defaultServer=$SERVER"
export CGO_ENABLED=1 CGO_CFLAGS="-mmacosx-version-min=13.0" CGO_LDFLAGS="-mmacosx-version-min=13.0"
for arch in arm64 amd64; do
  echo "== Agent ($arch)"
  GOOS=darwin GOARCH=$arch go build -trimpath -ldflags "$LD" -o "$OUT/tmp/agent-$arch" ./cmd/agent
done
lipo -create -output "$APP/Contents/MacOS/remoteMe" "$OUT/tmp/agent-arm64" "$OUT/tmp/agent-amd64"

sed "s/__VERSION__/$VERSION/g" packaging/macos/Info.plist > "$APP/Contents/Info.plist"

echo "== Symbol"
swift packaging/macos/make-icon.swift "$OUT/tmp/icon.png"
ICONSET="$OUT/tmp/AppIcon.iconset"; mkdir -p "$ICONSET"
for s in 16 32 128 256 512; do
  sips -z $s $s "$OUT/tmp/icon.png" --out "$ICONSET/icon_${s}x${s}.png" >/dev/null
  sips -z $((s*2)) $((s*2)) "$OUT/tmp/icon.png" --out "$ICONSET/icon_${s}x${s}@2x.png" >/dev/null
done
iconutil -c icns "$ICONSET" -o "$APP/Contents/Resources/AppIcon.icns"

echo "== Signatur ($IDENT)"
codesign --force --deep --options runtime -s "$IDENT" --identifier ist.fmatsch.remoteme "$APP"
codesign --verify --deep --strict "$APP" && echo "Signatur ok"

if [ $DMG -eq 1 ]; then
  hdiutil create -volname remoteMe -srcfolder "$APP" -ov -format UDZO "$OUT/remoteMe-$VERSION.dmg" >/dev/null
  echo "DMG: $OUT/remoteMe-$VERSION.dmg"
fi
rm -rf "$OUT/tmp"
echo "Fertig: $APP"
