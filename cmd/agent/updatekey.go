package main

// updatePublicKey prüft die Signatur von Updates (Gegenstück zum privaten Schlüssel des
// Herausgebers, siehe deploy/publish-update.sh). Eigene Builds: per -ldflags
// "-X main.updatePublicKey=..." ersetzen; leer schaltet Updates ab.
var updatePublicKey = "gUssmxey9IJG7CfnL1vdyfXsfryv2v0oCgOarh9lAbM="
