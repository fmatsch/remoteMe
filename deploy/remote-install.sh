#!/usr/bin/env bash
# Läuft auf dem Server (von deploy.sh hochgeladen). Umgebung: PORT, SITE (leer = Pfad /remote/ auf www.depes.ch).
set -euo pipefail

id remoteme >/dev/null 2>&1 || useradd --system --home /srv/remoteme --shell /usr/sbin/nologin remoteme
mkdir -p /srv/remoteme && chown -R remoteme:remoteme /srv/remoteme && chmod 700 /srv/remoteme
chown root:root /opt/remoteme && chmod 755 /opt/remoteme
mv -f /opt/remoteme/remoteme-server.new /opt/remoteme/remoteme-server
chown root:root /opt/remoteme/remoteme-server && chmod 755 /opt/remoteme/remoteme-server
systemctl daemon-reload
systemctl enable remoteme >/dev/null 2>&1
systemctl restart remoteme
sleep 1
echo "Dienst: $(systemctl is-active remoteme)"

# ---- Caddy (Sicherung, Prüfung, bei Fehler zurücksetzen) ----
TS=$(date +%Y%m%d%H%M%S)
if [ -n "${SITE:-}" ]; then
  F=/etc/caddy/remoteme.caddy
  [ -f "$F" ] && cp "$F" "$F.vor-update.$TS"
  cat > "$F" <<CADDY
# remoteMe – Relay-Server (Dienst remoteme, Port $PORT)
$SITE {
	encode gzip
	header {
		Strict-Transport-Security "max-age=31536000"
		-Server
	}
	reverse_proxy 127.0.0.1:$PORT
}
CADDY
  if ! grep -q 'import remoteme.caddy' /etc/caddy/Caddyfile; then
    cp /etc/caddy/Caddyfile "/etc/caddy/Caddyfile.vor-remoteme.$TS"
    echo 'import remoteme.caddy' >> /etc/caddy/Caddyfile
  fi
else
  F=/etc/caddy/depesche.caddy
  if ! grep -q 'remoteMe' "$F"; then
    cp "$F" "$F.vor-remoteme.$TS"
    python3 - "$F" "$PORT" <<'PY'
import sys
f, port = sys.argv[1], sys.argv[2]
s = open(f).read()
block = (
    "\t# remoteMe (Fernwartung): Dienst remoteme auf Port " + port + ", erreichbar unter /remote/\n"
    "\tredir /remote /remote/ 308\n"
    "\thandle_path /remote/* {\n"
    "\t\treverse_proxy 127.0.0.1:" + port + "\n"
    "\t}\n"
)
marker = "\t# alles andere (inkl. Startseite)"
assert marker in s, "Marker in depesche.caddy nicht gefunden"
open(f, "w").write(s.replace(marker, block + marker, 1))
PY
  fi
fi

if caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile >/tmp/caddy-validate.log 2>&1; then
  systemctl reload caddy && echo "Caddy neu geladen"
else
  echo "Caddy-Konfiguration ungültig - Änderungen werden zurückgenommen:"; tail -5 /tmp/caddy-validate.log
  [ -f "/etc/caddy/depesche.caddy.vor-remoteme.$TS" ] && cp "/etc/caddy/depesche.caddy.vor-remoteme.$TS" /etc/caddy/depesche.caddy
  [ -f "/etc/caddy/Caddyfile.vor-remoteme.$TS" ] && cp "/etc/caddy/Caddyfile.vor-remoteme.$TS" /etc/caddy/Caddyfile
  exit 1
fi
