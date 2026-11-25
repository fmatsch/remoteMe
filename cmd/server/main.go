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
		fmt.Fprintf(os.Stderr, "Aufruf:\n  server [Optionen]\n  server [Optionen] adduser <name> <passwort>\n  server [Optionen] setuser <name>   (Passwort von der Standardeingabe; anlegen oder ändern)\n  server [Optionen] deluser <name>\n  server [Optionen] totp-new <name>  (neues Geheimnis anzeigen, ändert nichts)\n  server [Optionen] totp-set <name>  (Standardeingabe: Geheimnis, dann Einmalcode)\n  server [Optionen] totp-off <name>\n\nVorher den laufenden Server stoppen.\n\nOptionen:\n")
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
	case "totp-new", "totp-set", "totp-off":
		if flag.NArg() != 2 {
			flag.Usage()
			os.Exit(2)
		}
		name := server.NormalizeName(flag.Arg(1))
		if !srv.Store().HasUser(name) {
			log.Fatal("Benutzer nicht gefunden")
		}
		switch flag.Arg(0) {
		case "totp-new":
			secret := server.NewTOTPSecret()
			fmt.Println(secret)
			fmt.Println(server.TOTPURI(name, secret))
		case "totp-set":
			in := bufio.NewScanner(os.Stdin)
			var lines []string
			for len(lines) < 2 && in.Scan() {
				lines = append(lines, strings.TrimSpace(in.Text()))
			}
			if len(lines) != 2 || lines[0] == "" {
				log.Fatal("erwartet: Geheimnis und Einmalcode auf der Standardeingabe")
			}
			if err := srv.Store().SetTOTP(name, lines[0], lines[1]); err != nil {
				log.Fatal(err)
			}
			fmt.Println("Einmalcode eingerichtet für:", name)
		case "totp-off":
			if err := srv.Store().SetTOTP(name, "", ""); err != nil {
				log.Fatal(err)
			}
			fmt.Println("Einmalcode abgeschaltet für:", name)
		}
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
