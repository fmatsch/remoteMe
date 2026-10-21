#ifndef FW_APP_DARWIN_H
#define FW_APP_DARWIN_H

// Menüleisten-App (läuft auf dem Hauptthread, blockiert bis zum Beenden).
void fwRunApp(void);
void fwQuitApp(void);
void fwRequestPermissions(void);
int fwAccessibilityTrusted(void);

#endif
