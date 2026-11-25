#!/usr/bin/env bash
# Läuft auf dem Server (von deploy.sh hochgeladen). Umgebung: PORT, SITE (eigene Domain).
set -euo pipefail
: "${SITE:?SITE fehlt}" "${PORT:?PORT fehlt}"

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
BACKUP=/root/remoteme-caddy-$TS
mkdir -p "$BACKUP" && cp /etc/caddy/Caddyfile /etc/caddy/*.caddy "$BACKUP"/

cat > /etc/caddy/remoteme.caddy <<CADDY
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
grep -q '^import remoteme.caddy' /etc/caddy/Caddyfile || echo 'import remoteme.caddy' >> /etc/caddy/Caddyfile

# Früherer Betrieb unter www.depes.ch/remote/: Nur bereits gekoppelte Zielrechner dürfen dort noch
# verbinden (eigenes Geheimnis, kein Cookie); alles andere wird auf die eigene Adresse umgeleitet.
if grep -q 'handle_path /remote/\*' /etc/caddy/depesche.caddy 2>/dev/null; then
  python3 - "$PORT" "$SITE" <<'PY'
import re, sys
port, site = sys.argv[1], sys.argv[2]
f = "/etc/caddy/depesche.caddy"
s = open(f).read()
block = (
    "\t# remoteMe ist umgezogen nach https://" + site + " - hier verbinden nur noch bestehende Zielrechner\n"
    "\thandle /remote/ws/agent {\n"
    "\t\turi strip_prefix /remote\n"
    "\t\treverse_proxy 127.0.0.1:" + port + "\n"
    "\t}\n"
    "\t@remoteme_alt path /remote /remote/*\n"
    "\thandle @remoteme_alt {\n"
    "\t\tredir https://" + site + "/ 302\n"
    "\t}\n"
)
pattern = r"\t# remoteMe \(Fernwartung\)[^\n]*\n\tredir /remote /remote/ 308\n\thandle_path /remote/\* \{\n\t\treverse_proxy [^\n]*\n\t\}\n"
s, n = re.subn(pattern, lambda m: block, s)
assert n == 1, "alter remoteMe-Block in depesche.caddy nicht wie erwartet"
open(f, "w").write(s)
PY
fi

if caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile >/tmp/caddy-validate.log 2>&1; then
  systemctl reload caddy && echo "Caddy neu geladen (Sicherung: $BACKUP)"
else
  echo "Caddy-Konfiguration ungültig - Änderungen werden zurückgenommen:"; tail -5 /tmp/caddy-validate.log
  cp "$BACKUP"/Caddyfile "$BACKUP"/*.caddy /etc/caddy/
  [ -f "$BACKUP/remoteme.caddy" ] || rm -f /etc/caddy/remoteme.caddy
  exit 1
fi
