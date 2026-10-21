package main

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

type ui struct {
	a     *Agent
	token string
	port  int
}

func newUI(a *Agent, ln net.Listener) *ui {
	b := make([]byte, 16)
	rand.Read(b)
	return &ui{a: a, token: hex.EncodeToString(b), port: ln.Addr().(*net.TCPAddr).Port}
}

func (u *ui) serve(ln net.Listener) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", u.page)
	mux.HandleFunc("GET /state", u.guard(u.state))
	mux.HandleFunc("POST /pair", u.guard(u.pair))
	mux.HandleFunc("POST /unpair", u.guard(u.unpair))
	mux.HandleFunc("POST /autostart", u.guard(u.autostart))
	http.Serve(ln, mux)
}

// guard schützt vor fremden Webseiten (DNS-Rebinding / CSRF): richtiger Host und Token nötig.
func (u *ui) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !u.hostOK(r) || r.Header.Get("X-Token") != u.token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func (u *ui) hostOK(r *http.Request) bool {
	return r.Host == fmt.Sprintf("127.0.0.1:%d", u.port) || r.Host == fmt.Sprintf("localhost:%d", u.port)
}

func (u *ui) page(w http.ResponseWriter, r *http.Request) {
	if !u.hostOK(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(bytes.Replace([]byte(setupHTML), []byte("{{TOKEN}}"), []byte(u.token), 1))
}

func jsonOut(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (u *ui) state(w http.ResponseWriter, r *http.Request) {
	a := u.a
	a.mu.Lock()
	st, em := a.status, a.errMsg
	cfg := a.cfg
	a.mu.Unlock()
	host, _ := os.Hostname()
	out := map[string]any{
		"status": st, "error": em, "autostart": autostartEnabled(),
		"defaultServer": defaultServer, "hostname": host, "os": runtime.GOOS, "version": version,
	}
	if cfg != nil {
		out["server"], out["username"], out["name"] = cfg.Server, cfg.Username, cfg.Name
		if !cfg.Approved {
			out["code"] = cfg.Code
		}
	}
	jsonOut(w, 200, out)
}

func (u *ui) pair(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Server, Username, Name string
		Autostart              bool
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&b) != nil {
		jsonOut(w, 400, map[string]string{"error": "Ungültige Eingabe"})
		return
	}
	server := strings.TrimRight(strings.TrimSpace(b.Server), "/")
	if !strings.Contains(server, "://") {
		server = "https://" + server
	}
	if b.Username = strings.TrimSpace(b.Username); b.Username == "" {
		jsonOut(w, 400, map[string]string{"error": "Bitte einen Benutzernamen eingeben"})
		return
	}
	host, _ := os.Hostname()
	if strings.TrimSpace(b.Name) != "" {
		host = strings.TrimSpace(b.Name)
	}
	body, _ := json.Marshal(map[string]string{"username": b.Username, "name": host, "os": runtime.GOOS})
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Post(server+"/api/agent/pair", "application/json", bytes.NewReader(body))
	if err != nil {
		jsonOut(w, 502, map[string]string{"error": "Server nicht erreichbar: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	var res struct{ ID, Secret, Code, Error string }
	json.NewDecoder(resp.Body).Decode(&res)
	if resp.StatusCode != 200 || res.ID == "" {
		msg := res.Error
		if msg == "" {
			msg = resp.Status
		}
		jsonOut(w, 400, map[string]string{"error": msg})
		return
	}
	if b.Autostart {
		setAutostart(true)
	}
	u.a.setConfig(&Config{Server: server, Username: strings.ToLower(b.Username), ID: res.ID, Secret: res.Secret, Name: host, Code: res.Code})
	jsonOut(w, 200, map[string]bool{"ok": true})
}

func (u *ui) unpair(w http.ResponseWriter, r *http.Request) {
	u.a.setConfig(nil)
	jsonOut(w, 200, map[string]bool{"ok": true})
}

func (u *ui) autostart(w http.ResponseWriter, r *http.Request) {
	var b struct{ On bool }
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 256)).Decode(&b)
	if err := setAutostart(b.On); err != nil {
		jsonOut(w, 500, map[string]string{"error": err.Error()})
		return
	}
	jsonOut(w, 200, map[string]bool{"ok": true})
}

const setupHTML = `<!doctype html>
<html lang="de"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>remoteMe - Einrichtung</title>
<style>
:root{--bg:#f4f6fa;--card:#fff;--fg:#1c2330;--mut:#667085;--acc:#2563eb;--ok:#16a34a;--bad:#dc2626;--line:#e3e8ef}
@media(prefers-color-scheme:dark){:root{--bg:#0f141b;--card:#171e28;--fg:#e6ebf2;--mut:#93a0b4;--line:#263041}}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--fg);font:15px/1.5 system-ui,Segoe UI,sans-serif;display:grid;place-items:center;min-height:100vh;padding:16px}
.card{background:var(--card);border:1px solid var(--line);border-radius:14px;padding:28px;width:100%;max-width:440px;box-shadow:0 8px 30px #0001}
h1{font-size:20px;margin:0 0 4px}p{color:var(--mut);margin:0 0 18px}
label{display:block;font-size:13px;font-weight:600;margin:12px 0 4px}
input[type=text]{width:100%;padding:10px 12px;border:1px solid var(--line);border-radius:8px;background:transparent;color:var(--fg);font:inherit}
input:focus{outline:2px solid var(--acc);border-color:transparent}
button{margin-top:18px;width:100%;padding:11px;border:0;border-radius:8px;background:var(--acc);color:#fff;font:inherit;font-weight:600;cursor:pointer}
button.sec{background:transparent;color:var(--mut);border:1px solid var(--line);margin-top:10px}
.chk{display:flex;gap:8px;align-items:center;margin-top:14px;font-size:14px}
.err{color:var(--bad);margin-top:12px;font-size:14px}
.code{font:700 44px/1 ui-monospace,Consolas,monospace;letter-spacing:.18em;text-align:center;padding:20px;background:var(--bg);border-radius:10px;margin:14px 0}
.st{display:flex;align-items:center;gap:8px;margin:10px 0}.dot{width:10px;height:10px;border-radius:50%;background:var(--mut)}
.dot.online{background:var(--ok)}.dot.error{background:var(--bad)}.dot.pending{background:#f59e0b}
.hide{display:none}
</style></head><body><div class="card">
<div id="setup" class="hide">
 <h1>remoteMe einrichten</h1><p>Dieser Computer soll von der Ferne aus erreichbar sein. Gib den Benutzer ein, der Zugriff bekommen soll.</p>
 <label>Server</label><input type="text" id="server" placeholder="https://remoteme.example.com">
 <label>Benutzer, der zugreifen darf</label><input type="text" id="user" autocomplete="off" placeholder="Benutzername">
 <label>Name dieses Computers</label><input type="text" id="name">
 <div class="chk"><input type="checkbox" id="auto" checked><label for="auto" style="margin:0;font-weight:400">Beim Anmelden automatisch starten</label></div>
 <div class="err" id="err"></div>
 <button id="go">Verbinden</button>
</div>
<div id="status" class="hide">
 <h1>remoteMe</h1>
 <div class="st"><span class="dot" id="dot"></span><b id="stText"></b></div>
 <div id="pend" class="hide"><p>Gib diesen Code in der Weboberfläche beim neuen Gerät ein, um den Zugriff freizugeben:</p><div class="code" id="code"></div></div>
 <p id="info"></p><div class="err" id="serr"></div>
 <div class="chk"><input type="checkbox" id="auto2"><label for="auto2" style="margin:0;font-weight:400">Beim Anmelden automatisch starten</label></div>
 <button class="sec" id="un">Zugriff widerrufen und Kopplung lösen</button>
</div></div>
<script>
const T="{{TOKEN}}",$=id=>document.getElementById(id);
const api=(p,m,b)=>fetch(p,{method:m||'GET',headers:{'X-Token':T,'Content-Type':'application/json'},body:b?JSON.stringify(b):undefined}).then(async r=>{const j=await r.json().catch(()=>({}));if(!r.ok)throw new Error(j.error||r.statusText);return j});
let first=true;
async function refresh(){
 let s;try{s=await api('/state')}catch(e){return}
 const paired=s.status!=='unpaired';
 $('setup').classList.toggle('hide',paired);$('status').classList.toggle('hide',!paired);
 if(!paired){
  if(first){$('server').value=s.defaultServer||'';$('name').value=s.hostname;first=false}
  if(s.error)$('err').textContent=s.error;
  return}
 first=false;
 const txt={connecting:'Verbinde mit Server ...',pending:'Wartet auf Freigabe',online:'Verbunden - Zugriff möglich',error:'Keine Verbindung'}[s.status]||s.status;
 $('stText').textContent=txt;$('dot').className='dot '+s.status;
 $('pend').classList.toggle('hide',!(s.code&&s.status!=='online'));$('code').textContent=s.code||'';
 $('info').textContent='Benutzer: '+s.username+' - Server: '+s.server;
 $('serr').textContent=s.status==='error'?s.error:'';
 $('auto2').checked=s.autostart;
}
$('go').onclick=async()=>{$('err').textContent='';
 try{await api('/pair','POST',{server:$('server').value,username:$('user').value,name:$('name').value,autostart:$('auto').checked});refresh()}
 catch(e){$('err').textContent=e.message}};
$('un').onclick=async()=>{if(confirm('Kopplung wirklich lösen? Danach ist kein Fernzugriff mehr möglich.')){await api('/unpair','POST');first=true;refresh()}};
$('auto2').onchange=()=>api('/autostart','POST',{on:$('auto2').checked}).catch(e=>alert(e.message));
refresh();setInterval(refresh,2000);
</script></body></html>`
