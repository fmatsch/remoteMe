#!/usr/bin/env bash
# Benutzer am remoteMe-Server anlegen, Passwort ändern oder Benutzer löschen.
#   deploy/user.sh                  anlegen bzw. Passwort ändern (fragt Name und Passwort ab)
#   deploy/user.sh --delete NAME    Benutzer samt seinen Computern löschen
# Das Passwort wird verdeckt abgefragt und nur über die verschlüsselte Verbindung als Eingabe
# übertragen (nicht in der Befehlshistorie oder Prozessliste). Der Dienst startet kurz neu.
set -euo pipefail

HOST="${PK_SERVER:-root@138.201.56.28}"
KEY="${PK_KEY:-$HOME/.ssh/praxiskalender_ed25519}"
SSH=(ssh -i "$KEY" -o IdentitiesOnly=yes -o BatchMode=yes "$HOST")

check_name() {
  [[ "$1" =~ ^[a-z0-9_.-]{3,32}$ && "$1" != -* ]] || { echo "Benutzername: 3-32 Zeichen (a-z, 0-9, _ . -)"; exit 1; }
}

# Führt einen Verwaltungsbefehl bei gestopptem Dienst aus und startet ihn in jedem Fall wieder.
remote() {
  "${SSH[@]}" "systemctl stop remoteme; trap 'systemctl start remoteme' EXIT; runuser -u remoteme -- /opt/remoteme/remoteme-server -data /srv/remoteme/data.json $1 '$2'"
}

if [ "${1:-}" = "--delete" ]; then
  NAME="${2:-}"; check_name "$NAME"
  read -r -p "Benutzer '$NAME' und seine Computer wirklich löschen? (ja/nein) " ANSWER
  [ "$ANSWER" = "ja" ] || { echo "Abgebrochen."; exit 0; }
  remote deluser "$NAME"
  exit 0
fi

read -r -p "Benutzername: " NAME
NAME=$(printf '%s' "$NAME" | tr '[:upper:]' '[:lower:]')
check_name "$NAME"
read -r -s -p "Neues Passwort (mindestens 8 Zeichen): " P1; echo
read -r -s -p "Passwort wiederholen: " P2; echo
[ "$P1" = "$P2" ] || { echo "Die Passwörter stimmen nicht überein."; exit 1; }
[ "${#P1}" -ge 8 ] || { echo "Das Passwort ist zu kurz."; exit 1; }
printf '%s\n' "$P1" | remote setuser "$NAME"
