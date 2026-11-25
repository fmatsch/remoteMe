//go:build windows

package main

// Windows-Dienst: macht den Rechner auch gesperrt, abgemeldet oder nach getrenntem Remotedesktop
// erreichbar.
//
// Aufbau: Der Dienst (Sitzung 0, Konto SYSTEM) startet in der aktiven Sitzung einen Helferprozess
// ("-helper") mit Systemrechten. Nur ein solcher Prozess darf Anmelde- und Sperrbildschirm
// (Winlogon-Desktop) aufnehmen und bedienen. Wechselt die Sitzung, wird der Helfer neu gestartet.
//
// Sicherheit: Programm unter "Programme", Konfiguration unter "ProgramData" – beides nur von
// Administratoren änderbar. Aus dem Suchpfad wird nichts gestartet.

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	serviceName = "remoteMe"
	sasEvent    = `Global\remoteMeSAS`
	policyKey   = `SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System`
	sasValue    = "SoftwareSASGeneration"
)

// machineMode: Prozess läuft als Teil des Dienstes (maschinenweite Konfiguration, SYSTEM).
var machineMode bool

const serviceSupported = true

var (
	pOpenInputDesktop = windows.NewLazySystemDLL("user32.dll").NewProc("OpenInputDesktop")
	pSetThreadDesktop = windows.NewLazySystemDLL("user32.dll").NewProc("SetThreadDesktop")
	pCloseDesktop     = windows.NewLazySystemDLL("user32.dll").NewProc("CloseDesktop")
	pSendSAS          = windows.NewLazySystemDLL("sas.dll").NewProc("SendSAS")
)

func knownFolder(id *windows.KNOWNFOLDERID, fallback string) string {
	p, err := windows.KnownFolderPath(id, 0)
	if err != nil {
		return fallback
	}
	return p
}

func programDir() string {
	return filepath.Join(knownFolder(windows.FOLDERID_ProgramFiles, `C:\Program Files`), "remoteMe")
}

func machineConfigDir() string {
	return filepath.Join(knownFolder(windows.FOLDERID_ProgramData, `C:\ProgramData`), "remoteMe")
}

func publicDesktop() string {
	return knownFolder(windows.FOLDERID_PublicDesktop, `C:\Users\Public\Desktop`)
}

func machineRoots() []entry {
	return []entry{
		{N: "Gemeinsamer Desktop", P: publicDesktop(), D: true},
		{N: "Benutzer", P: knownFolder(windows.FOLDERID_UserProfiles, `C:\Users`), D: true},
	}
}

// Offene Desktop-Handles je Thread: beim Wechsel wird das vorherige geschlossen.
var (
	deskMu sync.Mutex
	deskOf = map[uint32]uintptr{}
)

// useInputDesktop bindet den aktuellen Thread an den Desktop, der gerade sichtbar ist (normal,
// gesperrt oder Anmeldung). Der Thread bleibt dabei an die Goroutine gebunden.
func useInputDesktop() {
	if !machineMode {
		return
	}
	runtime.LockOSThread()
	const genericAll = 0x10000000
	h, _, _ := pOpenInputDesktop.Call(0, 0, genericAll)
	if h == 0 {
		return
	}
	if ok, _, _ := pSetThreadDesktop.Call(h); ok == 0 {
		pCloseDesktop.Call(h)
		return
	}
	tid := windows.GetCurrentThreadId()
	deskMu.Lock()
	old := deskOf[tid]
	deskOf[tid] = h
	deskMu.Unlock()
	if old != 0 {
		pCloseDesktop.Call(old)
	}
}

// sendSAS bittet den Dienst um Strg+Alt+Entf (nur er darf das auslösen).
func sendSAS() {
	if !machineMode {
		return
	}
	name, _ := windows.UTF16PtrFromString(sasEvent)
	h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
	if err != nil {
		log.Println("Strg+Alt+Entf:", err)
		return
	}
	defer windows.CloseHandle(h)
	windows.SetEvent(h)
}

// serviceInstalled darf auch ohne Administratorrechte abgefragt werden.
func serviceInstalled() bool {
	m, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return false
	}
	defer windows.CloseServiceHandle(m)
	name, _ := windows.UTF16PtrFromString(serviceName)
	s, err := windows.OpenService(m, name, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return false
	}
	windows.CloseServiceHandle(s)
	return true
}

// runElevated startet dieses Programm mit Administratorrechten (UAC-Abfrage).
func runElevated(args string) error {
	verb, _ := windows.UTF16PtrFromString("runas")
	exe, _ := windows.UTF16PtrFromString(exePath())
	a, _ := windows.UTF16PtrFromString(args)
	return windows.ShellExecute(0, verb, exe, a, nil, windows.SW_NORMAL)
}

// startServiceInstall wird von der Einrichtungsseite aufgerufen; die vorhandene Kopplung zieht um.
func startServiceInstall() error {
	if loadConfig() == nil {
		return errors.New("Bitte zuerst mit dem Server verbinden")
	}
	return runElevated(`-install -from "` + configPath() + `"`)
}

// quiet unterdrückt Meldungsfenster (unbeaufsichtigte Installation).
var quiet bool

func messageBox(text string, flags uint32) int32 {
	if quiet {
		log.Println(text)
		fmt.Println(text)
		return 0
	}
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString("remoteMe")
	r, _ := windows.MessageBox(0, t, c, flags)
	return r
}

// platformFlags meldet die Dienst-Befehle an; die Rückgabefunktion führt sie nach flag.Parse aus
// und liefert true, wenn das Programm damit fertig ist.
func platformFlags() func() bool {
	install := flag.Bool("install", false, "als Windows-Dienst installieren (Administrator)")
	uninstall := flag.Bool("uninstall", false, "Windows-Dienst entfernen (Administrator)")
	service := flag.Bool("service", false, "intern: Dienstprozess")
	helper := flag.Bool("helper", false, "intern: Helfer in der Benutzersitzung")
	from := flag.String("from", "", "intern: vorhandene Konfiguration übernehmen")
	flag.BoolVar(&quiet, "quiet", false, "ohne Meldungsfenster (unbeaufsichtigt)")
	return func() bool {
		switch {
		case *helper:
			machineMode = true
			return false // normaler Agent, ohne Einrichtungsseite (siehe main)
		case *service:
			machineMode = true
			runService()
		case *install:
			adminCommand("-install -from \""+*from+"\"", func() error { return installService(*from) },
				"remoteMe läuft jetzt als Dienst. Der Rechner ist auch gesperrt und nach der Abmeldung erreichbar.")
		case *uninstall:
			adminCommand("-uninstall", uninstallService, "Der remoteMe-Dienst wurde entfernt. Zum erneuten Verwenden neu einrichten.")
		default:
			if !serviceInstalled() {
				return false
			}
			// Läuft als Dienst: keine zweite Instanz in der Benutzersitzung.
			bg := flag.Lookup("background")
			if bg == nil || bg.Value.String() != "true" {
				const yesNo, question, second = 0x4, 0x20, 0x100
				// Von woanders gestartet (z. B. neu heruntergeladen): zuerst Aktualisierung anbieten.
				if !strings.EqualFold(exePath(), filepath.Join(programDir(), "remoteMe.exe")) &&
					messageBox("remoteMe ist als Dienst installiert.\n\nSoll der Dienst mit dieser Version ("+version+") aktualisiert werden? Die Kopplung bleibt erhalten.", yesNo|question) == 6 {
					runElevated("-install")
					return true
				}
				if messageBox("remoteMe läuft als Dienst im Hintergrund.\n\nSoll der Dienst entfernt werden?", yesNo|question|second) == 6 {
					runElevated("-uninstall")
				}
			}
		}
		return true
	}
}

func adminCommand(args string, run func() error, done string) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		if err := runElevated(args); err != nil {
			messageBox("Dafür sind Administratorrechte nötig: "+err.Error(), 0x10)
		}
		return
	}
	if err := run(); err != nil {
		messageBox("Fehlgeschlagen: "+err.Error(), 0x10)
		os.Exit(1)
	}
	messageBox(done, 0x40)
}

// ---- Installation ----

// protectDir beschränkt den Ordner auf System und Administratoren (ohne Vererbung von oben).
func protectDir(path string) error {
	sd, err := windows.SecurityDescriptorFromString("O:BAD:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)")
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		owner, nil, dacl, nil)
}

func copyFile(src, dst string) error {
	if strings.EqualFold(filepath.Clean(src), filepath.Clean(dst)) {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func stopService(s *mgr.Service) {
	s.Control(svc.Stop)
	for i := 0; i < 50; i++ {
		st, err := s.Query()
		if err != nil || st.State == svc.Stopped {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func installService(from string) error {
	var cfg []byte
	if from != "" {
		b, err := os.ReadFile(from)
		if err != nil {
			return fmt.Errorf("Konfiguration nicht lesbar: %w", err)
		}
		cfg = b
	}
	data := machineConfigDir()
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	existing, openErr := m.OpenService(serviceName)
	had := openErr == nil
	if had {
		stopService(existing)
		defer existing.Close()
	}
	if cfg == nil {
		if _, err := os.Stat(filepath.Join(data, "agent.json")); err != nil || !had {
			return errors.New("Bitte remoteMe zuerst normal starten, mit dem Server verbinden und dann dort 'Als Dienst installieren' wählen")
		}
	}
	// Andere laufende Instanzen beenden (sie würden die Programmdatei sperren).
	sys := knownFolder(windows.FOLDERID_System, `C:\Windows\System32`)
	kill := exec.Command(filepath.Join(sys, "taskkill.exe"), "/F", "/IM", "remoteMe.exe", "/FI", "PID ne "+strconv.Itoa(os.Getpid()))
	hideWindow(kill)
	kill.Run()
	time.Sleep(time.Second)

	// Datenordner: bei Neuinstallation frisch anlegen, damit niemand ihn vorbereitet haben kann.
	if !had {
		if err := os.RemoveAll(data); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(data, 0o700); err != nil {
		return err
	}
	if err := protectDir(data); err != nil {
		return fmt.Errorf("Ordner schützen: %w", err)
	}
	if cfg != nil {
		if err := os.WriteFile(filepath.Join(data, "agent.json"), cfg, 0o600); err != nil {
			return err
		}
	}

	prog := programDir()
	if err := os.MkdirAll(prog, 0o755); err != nil {
		return err
	}
	self := exePath()
	exe := filepath.Join(prog, "remoteMe.exe")
	if err := copyFile(self, exe); err != nil {
		return err
	}
	for _, name := range []string{"ffmpeg.exe", "ffmpeg-LICENSE.txt", "ffmpeg-README.txt", "LICENSE.txt", "LIESMICH.txt"} {
		src := filepath.Join(filepath.Dir(self), name)
		if _, err := os.Stat(src); err == nil {
			if err := copyFile(src, filepath.Join(prog, name)); err != nil {
				return err
			}
		}
	}

	// Strg+Alt+Entf durch Dienste erlauben (vorherigen Wert für die Deinstallation merken).
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, policyKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	prevFile := filepath.Join(data, "sas.prev")
	if _, err := os.Stat(prevFile); err != nil {
		prev := "-"
		if v, _, err := k.GetIntegerValue(sasValue); err == nil {
			prev = strconv.FormatUint(v, 10)
		}
		os.WriteFile(prevFile, []byte(prev), 0o600)
	}
	if err := k.SetDWordValue(sasValue, 1); err != nil {
		return err
	}

	s := existing
	if !had {
		s, err = m.CreateService(serviceName, exe, mgr.Config{
			DisplayName: "remoteMe Fernwartung",
			Description: "Macht diesen Computer für die Fernwartung mit remoteMe erreichbar.",
			StartType:   mgr.StartAutomatic,
		}, "-service")
		if err != nil {
			return err
		}
		defer s.Close()
	}
	s.SetRecoveryActions([]mgr.RecoveryAction{{Type: mgr.ServiceRestart, Delay: 5 * time.Second}}, 86400)
	if err := s.Start(); err != nil {
		return fmt.Errorf("Dienst starten: %w", err)
	}
	// Die Einrichtung in der Benutzersitzung wird nicht mehr gebraucht.
	winAutostartSet(false, "")
	if from != "" {
		os.Remove(from)
	}
	return nil
}

func uninstallService() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(serviceName)
	if err != nil {
		return errors.New("der Dienst ist nicht installiert")
	}
	defer s.Close()
	stopService(s)
	if err := s.Delete(); err != nil {
		return err
	}
	data := machineConfigDir()
	if b, err := os.ReadFile(filepath.Join(data, "sas.prev")); err == nil {
		if k, err := registry.OpenKey(registry.LOCAL_MACHINE, policyKey, registry.SET_VALUE); err == nil {
			if v, err := strconv.ParseUint(string(b), 10, 32); err == nil {
				k.SetDWordValue(sasValue, uint32(v))
			} else {
				k.DeleteValue(sasValue)
			}
			k.Close()
		}
	}
	return os.RemoveAll(data) // Kopplung und Protokoll
}

// ---- Dienstprozess ----

type service struct{}

func runService() {
	if lf, err := os.OpenFile(filepath.Join(machineConfigDir(), "service.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		if st, _ := lf.Stat(); st != nil && st.Size() > 1<<20 {
			lf.Truncate(0)
		}
		log.SetOutput(lf)
	}
	if err := svc.Run(serviceName, &service{}); err != nil {
		log.Println("Dienst:", err)
	}
}

// activeSession: eine aktive Sitzung (Konsole oder Remotedesktop), sonst die Konsole mit Anmeldebildschirm.
func activeSession() uint32 {
	console := windows.WTSGetActiveConsoleSessionId()
	var info *windows.WTS_SESSION_INFO
	var n uint32
	if err := windows.WTSEnumerateSessions(0, 0, 1, &info, &n); err != nil {
		return console
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(info)))
	best := console
	for _, s := range unsafe.Slice(info, n) {
		if s.State == windows.WTSActive && s.SessionID != 0 {
			if s.SessionID == console {
				return console
			}
			best = s.SessionID
		}
	}
	return best
}

// startHelper startet den Helfer mit Systemrechten in der angegebenen Sitzung. Er hängt an einem
// Job-Objekt und endet damit sicher samt Unterprozessen (ffmpeg).
func startHelper(session uint32) (process, job windows.Handle, err error) {
	var self windows.Token
	if err = windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ALL_ACCESS, &self); err != nil {
		return
	}
	defer self.Close()
	var token windows.Token
	if err = windows.DuplicateTokenEx(self, windows.MAXIMUM_ALLOWED, nil, windows.SecurityImpersonation, windows.TokenPrimary, &token); err != nil {
		return
	}
	defer token.Close()
	if err = windows.SetTokenInformation(token, windows.TokenSessionId, (*byte)(unsafe.Pointer(&session)), uint32(unsafe.Sizeof(session))); err != nil {
		return
	}
	if job, err = windows.CreateJobObject(nil, nil); err != nil {
		return
	}
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		windows.CloseHandle(job)
		return
	}
	exe := exePath()
	app, _ := windows.UTF16PtrFromString(exe)
	cmd, _ := windows.UTF16PtrFromString(`"` + exe + `" -helper`)
	dir, _ := windows.UTF16PtrFromString(filepath.Dir(exe))
	desktop, _ := windows.UTF16PtrFromString(`winsta0\default`)
	si := windows.StartupInfo{Desktop: desktop}
	si.Cb = uint32(unsafe.Sizeof(si))
	var pi windows.ProcessInformation
	if err = windows.CreateProcessAsUser(token, app, cmd, nil, nil, false, windows.CREATE_SUSPENDED|windows.CREATE_NO_WINDOW, nil, dir, &si, &pi); err != nil {
		windows.CloseHandle(job)
		return
	}
	if err = windows.AssignProcessToJobObject(job, pi.Process); err != nil {
		windows.TerminateProcess(pi.Process, 1)
		windows.CloseHandle(pi.Thread)
		windows.CloseHandle(pi.Process)
		windows.CloseHandle(job)
		return
	}
	windows.ResumeThread(pi.Thread)
	windows.CloseHandle(pi.Thread)
	return pi.Process, job, nil
}

func (*service) Execute(_ []string, r <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	name, _ := windows.UTF16PtrFromString(sasEvent)
	sas, err := windows.CreateEvent(nil, 0, 0, name)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		log.Println("Ereignis für Strg+Alt+Entf:", err)
	}
	var process, job windows.Handle
	var session uint32
	var started time.Time
	var pause time.Time // nach sofortigem Absturz nicht ununterbrochen neu starten
	stop := func() {
		if job != 0 {
			windows.CloseHandle(job) // beendet den Helfer
			windows.CloseHandle(process)
			job, process = 0, 0
		}
	}
	defer stop()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown | svc.AcceptSessionChange}

	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case c := <-r:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				return false, 0
			}
		case <-tick.C:
		}
		if sas != 0 {
			if ev, _ := windows.WaitForSingleObject(sas, 0); ev == windows.WAIT_OBJECT_0 {
				if err := pSendSAS.Find(); err == nil {
					pSendSAS.Call(0)
				}
			}
		}
		want := activeSession()
		if want == 0xFFFFFFFF {
			continue // gerade keine Sitzung (Umschalten)
		}
		if job != 0 {
			ev, _ := windows.WaitForSingleObject(process, 0)
			exited := ev != uint32(windows.WAIT_TIMEOUT)
			if !exited && want == session {
				continue
			}
			if exited && time.Since(started) < 10*time.Second {
				pause = time.Now().Add(15 * time.Second)
			}
			stop()
		}
		if time.Now().Before(pause) {
			continue
		}
		p, j, err := startHelper(want)
		if err != nil {
			log.Printf("Helfer in Sitzung %d: %v", want, err)
			time.Sleep(3 * time.Second)
			continue
		}
		process, job, session, started = p, j, want, time.Now()
		log.Printf("Helfer in Sitzung %d gestartet", want)
	}
}
