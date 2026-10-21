package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"

	"fernwartung/internal/server"
)

func main() {
	addr := flag.String("addr", ":8080", "Adresse, auf der der Server lauscht")
	data := flag.String("data", "fernwartung-data.json", "Datei für Benutzer und Geräte")
	cert := flag.String("cert", "", "TLS-Zertifikat (optional, sonst Reverse-Proxy verwenden)")
	key := flag.String("key", "", "TLS-Schlüssel")
	open := flag.Bool("open-registration", false, "Registrierung für jeden erlauben (sonst nur der erste Benutzer bzw. per 'adduser')")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Aufruf:\n  server [Optionen]\n  server [Optionen] adduser <name> <passwort>\n\nOptionen:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	srv, err := server.New(server.Config{Addr: *addr, DataFile: *data, CertFile: *cert, KeyFile: *key, OpenRegistration: *open})
	if err != nil {
		log.Fatal(err)
	}
	if flag.Arg(0) == "adduser" {
		if flag.NArg() != 3 {
			flag.Usage()
			os.Exit(2)
		}
		if err := srv.Store().AddUser(flag.Arg(1), flag.Arg(2)); err != nil {
			log.Fatal(err)
		}
		fmt.Println("Benutzer angelegt:", server.NormalizeName(flag.Arg(1)))
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := srv.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
