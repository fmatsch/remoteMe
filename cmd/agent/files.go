package main

import (
	"archive/zip"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

func homeDir() string {
	h, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return h
}

// userDir sucht einen Standardordner (Desktop, Downloads, Dokumente).
func userDir(names ...string) string {
	h := homeDir()
	for _, n := range names {
		for _, base := range []string{h, filepath.Join(h, "OneDrive")} {
			p := filepath.Join(base, n)
			if st, err := os.Stat(p); err == nil && st.IsDir() {
				return p
			}
		}
	}
	return h
}

func desktopDir() string { return userDir("Desktop", "Schreibtisch") }

type entry struct {
	N string `json:"n"`
	P string `json:"p"`
	D bool   `json:"d"`
	S int64  `json:"s"`
	M int64  `json:"m"`
}

func listDir(p string) map[string]any {
	res := map[string]any{"t": "ls-res", "path": p}
	if p == "" {
		res["parent"] = ""
		roots := []entry{
			{N: "Desktop", P: desktopDir(), D: true},
			{N: "Downloads", P: userDir("Downloads"), D: true},
			{N: "Dokumente", P: userDir("Documents", "Dokumente"), D: true},
			{N: "Benutzerordner", P: homeDir(), D: true},
		}
		if runtime.GOOS == "windows" {
			roots = append(roots, probeDrives()...)
		} else {
			roots = append(roots, entry{N: "/", P: "/", D: true})
		}
		res["entries"] = roots
		return res
	}
	p = filepath.Clean(p)
	res["path"] = p
	parent := filepath.Dir(p)
	if parent == p {
		parent = ""
	}
	res["parent"] = parent
	des, err := os.ReadDir(p)
	if err != nil {
		res["err"] = errText(err)
		res["entries"] = []entry{}
		return res
	}
	out := make([]entry, 0, len(des))
	for _, de := range des {
		e := entry{N: de.Name(), P: filepath.Join(p, de.Name()), D: de.IsDir()}
		if info, err := de.Info(); err == nil {
			e.S, e.M = info.Size(), info.ModTime().Unix()
			if info.Mode()&fs.ModeSymlink != 0 {
				if st, err := os.Stat(e.P); err == nil {
					e.D = st.IsDir()
				}
			}
		}
		out = append(out, e)
		if len(out) >= 5000 {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].D != out[j].D {
			return out[i].D
		}
		return strings.ToLower(out[i].N) < strings.ToLower(out[j].N)
	})
	res["entries"] = out
	return res
}

// probeDrives prüft alle Laufwerksbuchstaben parallel; nicht erreichbare Netzlaufwerke
// würden sonst Sekunden blockieren und werden nach 700 ms übergangen.
func probeDrives() []entry {
	var mu sync.Mutex
	var out []entry
	var wg sync.WaitGroup
	for c := 'A'; c <= 'Z'; c++ {
		wg.Add(1)
		go func(c rune) {
			defer wg.Done()
			d := string(c) + `:\`
			ok := make(chan bool, 1)
			go func() { _, err := os.Stat(d); ok <- err == nil }()
			select {
			case good := <-ok:
				if good {
					mu.Lock()
					out = append(out, entry{N: "Laufwerk " + string(c) + ":", P: d, D: true})
					mu.Unlock()
				}
			case <-time.After(700 * time.Millisecond):
			}
		}(c)
	}
	wg.Wait()
	sort.Slice(out, func(i, j int) bool { return out[i].P < out[j].P })
	return out
}

func errText(err error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		err = pe.Err
	}
	return err.Error()
}

// ---- Übertragungen ----

type upload struct {
	f     *os.File
	part  string
	final string
	err   error
	n     int64
}

type transfers struct {
	mu sync.Mutex
	ul map[uint32]*upload
	dl map[uint32]context.CancelFunc
}

func newTransfers() *transfers {
	return &transfers{ul: map[uint32]*upload{}, dl: map[uint32]context.CancelFunc{}}
}

func (t *transfers) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, u := range t.ul {
		u.f.Close()
		os.Remove(u.part)
		delete(t.ul, id)
	}
	for id, c := range t.dl {
		c()
		delete(t.dl, id)
	}
}

func uniquePath(dir, name string) string {
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); err != nil {
		return p
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; ; i++ {
		p = filepath.Join(dir, fmt.Sprintf("%s (%d)%s", base, i, ext))
		if _, err := os.Stat(p); err != nil {
			return p
		}
	}
}

func (t *transfers) begin(m *Msg, a *Agent) {
	fail := func(err error) {
		a.sendJSON(map[string]any{"t": "ul-done", "req": m.Req, "err": errText(err)})
	}
	name := filepath.Base(strings.ReplaceAll(m.Name, "\\", "/"))
	if name == "." || name == "/" || name == "" {
		fail(errors.New("ungültiger Dateiname"))
		return
	}
	dir := m.Dir
	if dir == "" {
		dir = desktopDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fail(err)
		return
	}
	final := uniquePath(dir, name)
	part := final + ".fwpart"
	f, err := os.Create(part)
	if err != nil {
		fail(err)
		return
	}
	t.mu.Lock()
	t.ul[m.Req] = &upload{f: f, part: part, final: final}
	t.mu.Unlock()
}

func (t *transfers) chunk(req uint32, data []byte, a *Agent) {
	t.mu.Lock()
	u := t.ul[req]
	t.mu.Unlock()
	if u == nil || u.err != nil {
		return
	}
	if _, err := u.f.Write(data); err != nil {
		u.err = err
		a.sendJSON(map[string]any{"t": "ul-done", "req": req, "err": errText(err)})
		t.abort(req)
	}
}

func (t *transfers) end(req uint32, a *Agent) {
	t.mu.Lock()
	u := t.ul[req]
	delete(t.ul, req)
	t.mu.Unlock()
	if u == nil {
		return
	}
	err := u.f.Close()
	if err == nil {
		err = os.Rename(u.part, u.final)
	}
	if err != nil {
		os.Remove(u.part)
		a.sendJSON(map[string]any{"t": "ul-done", "req": req, "err": errText(err)})
		return
	}
	a.sendJSON(map[string]any{"t": "ul-done", "req": req, "path": u.final})
}

func (t *transfers) abort(req uint32) {
	t.mu.Lock()
	u := t.ul[req]
	delete(t.ul, req)
	t.mu.Unlock()
	if u != nil {
		u.f.Close()
		os.Remove(u.part)
	}
}

func (t *transfers) cancel(req uint32) {
	t.mu.Lock()
	c := t.dl[req]
	delete(t.dl, req)
	t.mu.Unlock()
	if c != nil {
		c()
	}
}

func (t *transfers) download(parent context.Context, m *Msg, a *Agent) {
	ctx, cancel := context.WithCancel(parent)
	t.mu.Lock()
	t.dl[m.Req] = cancel
	t.mu.Unlock()
	go func() {
		defer t.cancel(m.Req)
		req := m.Req
		fail := func(err error) {
			a.sendJSON(map[string]any{"t": "dl-err", "req": req, "err": errText(err)})
		}
		st, err := os.Stat(m.Path)
		if err != nil {
			fail(err)
			return
		}
		var r io.Reader
		name, size := filepath.Base(m.Path), st.Size()
		if st.IsDir() {
			pr, pw := io.Pipe()
			go func() { pw.CloseWithError(zipDir(ctx, pw, m.Path)) }()
			defer pr.Close()
			r, name, size = pr, name+".zip", 0
		} else {
			f, err := os.Open(m.Path)
			if err != nil {
				fail(err)
				return
			}
			defer f.Close()
			r = f
		}
		a.sendJSON(map[string]any{"t": "dl-head", "req": req, "name": name, "size": size})
		buf := make([]byte, 5+64*1024)
		buf[0] = 0x03
		binary.BigEndian.PutUint32(buf[1:5], req)
		for ctx.Err() == nil {
			n, err := r.Read(buf[5:])
			if n > 0 {
				if a.send(websocket.BinaryMessage, buf[:5+n]) != nil {
					return
				}
			}
			if err == io.EOF {
				a.sendJSON(map[string]any{"t": "dl-end", "req": req})
				return
			}
			if err != nil {
				fail(err)
				return
			}
		}
	}()
}

func zipDir(ctx context.Context, w io.Writer, root string) error {
	zw := zip.NewWriter(w)
	base := filepath.Dir(root)
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || (!d.IsDir() && !d.Type().IsRegular()) {
			return nil // unlesbare Einträge und Links überspringen
		}
		rel, _ := filepath.Rel(base, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			_, err := zw.Create(rel + "/")
			return err
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		fw, err := zw.Create(rel)
		if err != nil {
			return err
		}
		_, err = io.Copy(fw, f)
		return err
	})
	if err != nil {
		return err
	}
	return zw.Close()
}
