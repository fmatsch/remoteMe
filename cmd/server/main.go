package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"

	"remoteme/internal/server"
)

func main() {
	addr := flag.String("addr", ":8080", "Adresse, auf der der Server lauscht")
	data := flag.String("data", "remoteme-data.json", "Datei für Benutzer und Geräte")
	cert := flag.String("cert", "", "TLS-Zertifikat (optional, sonst Reverse-Proxy verwenden)")
	key := flag.String("key", "", "TLS-Schlüssel")
	open := flag.Bool("open-registration", false, "Registrierung für jeden erlauben (sonst nur der erste Benutzer bzw. per 'adduser')")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Aufruf:\n  server [Optionen]\n  server [Optionen] adduser <name> <passwort>\n  server [Optionen] setuser <name>   (Passwort von der Standardeingabe; anlegen oder ändern)\n  server [Optionen] deluser <name>\n\nVorher den laufenden Server stoppen.\n\nOptionen:\n")
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
	switch flag.Arg(0) {
	case "setuser":
		if flag.NArg() != 2 {
			flag.Usage()
			os.Exit(2)
		}
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			log.Fatal("kein Passwort auf der Standardeingabe")
		}
		created, err := srv.Store().SetPassword(flag.Arg(1), strings.TrimRight(line, "\r\n"))
		if err != nil {
			log.Fatal(err)
		}
		if created {
			fmt.Println("Benutzer angelegt:", server.NormalizeName(flag.Arg(1)))
		} else {
			fmt.Println("Passwort geändert:", server.NormalizeName(flag.Arg(1)))
		}
		return
	case "deluser":
		if flag.NArg() != 2 {
			flag.Usage()
			os.Exit(2)
		}
		if err := srv.Store().DeleteUser(flag.Arg(1)); err != nil {
			log.Fatal(err)
		}
		fmt.Println("Benutzer gelöscht:", server.NormalizeName(flag.Arg(1)))
		return
	case "":
	default:
		flag.Usage()
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := srv.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
