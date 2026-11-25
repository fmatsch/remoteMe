#!/usr/bin/env bash
# Benutzer am remoteMe-Server verwalten.
#   deploy/user.sh                    anlegen bzw. Passwort ändern (fragt Name und Passwort ab)
#   deploy/user.sh --totp NAME        Authenticator-App als zweiten Faktor einrichten (QR-Code)
#   deploy/user.sh --totp-off NAME    zweiten Faktor abschalten (z. B. Telefon verloren)
#   deploy/user.sh --delete NAME      Benutzer samt seinen Computern löschen
# Geheimnisse werden nur über die verschlüsselte Verbindung als Eingabe übertragen (nicht in der
# Befehlshistorie oder Prozessliste). Bei Änderungen startet der Dienst kurz neu.
set -euo pipefail

HOST="${PK_SERVER:-root@138.201.56.28}"
KEY="${PK_KEY:-$HOME/.ssh/praxiskalender_ed25519}"
DIR="$(cd "$(dirname "$0")" && pwd)"
SSH=(ssh -i "$KEY" -o IdentitiesOnly=yes -o BatchMode=yes "$HOST")
BIN="runuser -u remoteme -- /opt/remoteme/remoteme-server -data /srv/remoteme/data.json"

check_name() {
  [[ "$1" =~ ^[a-z0-9_.-]{3,32}$ && "$1" != -* ]] || { echo "Benutzername: 3-32 Zeichen (a-z, 0-9, _ . -)"; exit 1; }
}

# Führt eine Änderung bei gestopptem Dienst aus und startet ihn in jedem Fall wieder.
remote() {
  "${SSH[@]}" "systemctl stop remoteme; trap 'systemctl start remoteme' EXIT; $BIN $1 '$2'"
}

case "${1:-}" in
  --delete)
    NAME="${2:-}"; check_name "$NAME"
    read -r -p "Benutzer '$NAME' und seine Computer wirklich löschen? (ja/nein) " ANSWER
    [ "$ANSWER" = "ja" ] || { echo "Abgebrochen."; exit 0; }
    remote deluser "$NAME"
    ;;
  --totp)
    NAME="${2:-}"; check_name "$NAME"
    OUT=$("${SSH[@]}" "$BIN totp-new '$NAME'")
    SECRET=$(printf '%s\n' "$OUT" | sed -n 1p)
    URI=$(printf '%s\n' "$OUT" | sed -n 2p)
    echo "Scanne diesen Code mit deiner Authenticator-App (Google/Microsoft Authenticator, Passwörter-App …):"
    echo
    printf '%s\n' "$URI" | swift "$DIR/qr.swift"
    echo
    echo "Falls das Scannen nicht geht, diesen Schlüssel eintippen: $SECRET"
    echo
    read -r -p "6-stelliger Code aus der App zur Bestätigung: " CODE
    printf '%s\n%s\n' "$SECRET" "$CODE" | remote totp-set "$NAME"
    clear 2>/dev/null || true # QR-Code und Schlüssel nicht stehen lassen
    echo "Zweiter Faktor für '$NAME' ist aktiv. Ab jetzt verlangt die Anmeldung den Einmalcode."
    ;;
  --totp-off)
    NAME="${2:-}"; check_name "$NAME"
    remote totp-off "$NAME"
    ;;
  "")
    read -r -p "Benutzername: " NAME
    NAME=$(printf '%s' "$NAME" | tr '[:upper:]' '[:lower:]')
    check_name "$NAME"
    read -r -s -p "Neues Passwort (mindestens 8 Zeichen): " P1; echo
    read -r -s -p "Passwort wiederholen: " P2; echo
    [ "$P1" = "$P2" ] || { echo "Die Passwörter stimmen nicht überein."; exit 1; }
    [ "${#P1}" -ge 8 ] || { echo "Das Passwort ist zu kurz."; exit 1; }
    printf '%s\n' "$P1" | remote setuser "$NAME"
    ;;
  *)
    sed -n '2,8p' "$0"; exit 1
    ;;
esac
