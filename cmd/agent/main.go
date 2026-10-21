package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"time"
)

// defaultServer wird beim Bauen per -ldflags "-X main.defaultServer=https://..." vorbelegt.
var defaultServer = ""

const uiPort = 17321

func main() {
	background := flag.Bool("background", false, "ohne Browserfenster starten (Autostart)")
	server := flag.String("server", "", "Server-Adresse vorbelegen")
	flag.Parse()
	if *server != "" {
		defaultServer = *server
	}

	if lf, err := os.OpenFile(filepath.Join(configDir(), "agent.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		if st, _ := lf.Stat(); st != nil && st.Size() > 1<<20 {
			lf.Truncate(0)
		}
		log.SetOutput(lf)
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", uiPort))
	if err != nil {
		// Läuft schon: nur die Einrichtungsseite öffnen.
		if !*background {
			openBrowser(fmt.Sprintf("http://127.0.0.1:%d/", uiPort))
		}
		return
	}

	a := newAgent()
	u := newUI(a, ln)
	go u.serve(ln)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	go a.run(ctx)
	log.Printf("Agent %s gestartet", version)

	showSetup := !*background || a.config() == nil
	if showSetup && !nativeUI {
		time.Sleep(300 * time.Millisecond)
		openBrowser(fmt.Sprintf("http://127.0.0.1:%d/", uiPort))
	}
	runApp(ctx, stop, a, showSetup)
}
