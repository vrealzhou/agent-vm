package container

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/vrealzhou/agent-vm/internal/config"
)

type containerInfo struct {
	Name        string `json:"name"`
	Running     bool   `json:"running"`
	WebRunning  bool   `json:"web_running"`
	TermRunning bool   `json:"term_running"`
	Workspace   string `json:"workspace"`
}

func getContainerInfos() []containerInfo {
	names := config.ListManagedContainers()
	var infos []containerInfo
	for _, name := range names {
		running := IsRunning(name)
		infos = append(infos, containerInfo{
			Name:        name,
			Running:     running,
			WebRunning:  running && PortReady(name, opencodeWebPort),
			TermRunning: running && PortReady(name, ttydPort),
			Workspace:   config.LoadWorkspace(name),
		})
	}
	return infos
}

func Web(port int) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/containers/", apiContainerActionHandler)
	mux.HandleFunc("/api/containers", apiContainersHandler)
	mux.HandleFunc("/", rootHandler)

	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("[agent-vm] web portal on http://localhost:%d\n", port)
	return http.ListenAndServe(addr, mux)
}

func rootHandler(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if i := strings.LastIndex(host, ":"); i != -1 {
		host = host[:i]
	}

	if strings.HasSuffix(host, "-term.localhost") {
		name := strings.TrimSuffix(host, "-term.localhost")
		if name != "" {
			proxyToContainer(w, r, name, ttydPort, ensureTTYD)
			return
		}
	}
	if strings.HasSuffix(host, ".localhost") {
		name := strings.TrimSuffix(host, ".localhost")
		if name != "" && name != "www" {
			proxyToContainer(w, r, name, opencodeWebPort, ensureOpencodeWeb)
			return
		}
	}
	servePortal(w)
}

func proxyToContainer(w http.ResponseWriter, r *http.Request, name string, port int, ensure func(string) error) {
	if config.LoadWorkspace(name) == "" {
		http.Error(w, fmt.Sprintf("container %q is not managed by agent-vm", name), http.StatusNotFound)
		return
	}
	if !IsRunning(name) {
		http.Error(w, fmt.Sprintf("container %q is not running", name), http.StatusServiceUnavailable)
		return
	}
	if !PortReady(name, port) {
		if err := ensure(name); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
	}

	target := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", port)}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return dialContainer(ctx, name, port)
		},
	}
	origDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		origDirector(req)
		req.Host = target.Host
	}
	proxy.ServeHTTP(w, r)
}

func apiContainersHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(getContainerInfos())
}

func apiContainerActionHandler(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/api/containers/"), "/", 2)
	if len(parts) != 2 || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	name := parts[0]
	if config.LoadWorkspace(name) == "" {
		http.Error(w, fmt.Sprintf("container %q is not managed by agent-vm", name), http.StatusNotFound)
		return
	}

	var ensure func(string) error
	switch parts[1] {
	case "start-web":
		ensure = ensureOpencodeWeb
	case "start-terminal":
		ensure = ensureTTYD
	default:
		http.NotFound(w, r)
		return
	}

	if err := ensure(name); err != nil {
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"name": name, "ok": true})
}

func servePortal(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(portalHTML))
}

// ── Container tunnel via container exec + socat ──

type pipeConn struct {
	stdin  io.WriteCloser
	stdout io.ReadCloser
	cmd    *exec.Cmd
}

func (c *pipeConn) Read(b []byte) (int, error)  { return c.stdout.Read(b) }
func (c *pipeConn) Write(b []byte) (int, error) { return c.stdin.Write(b) }

func (c *pipeConn) Close() error {
	c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_ = c.cmd.Wait()
	c.stdout.Close()
	return nil
}

func (c *pipeConn) LocalAddr() net.Addr              { return nil }
func (c *pipeConn) RemoteAddr() net.Addr             { return nil }
func (c *pipeConn) SetDeadline(time.Time) error      { return nil }
func (c *pipeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *pipeConn) SetWriteDeadline(time.Time) error { return nil }

func dialContainer(ctx context.Context, name string, port int) (net.Conn, error) {
	cmd := exec.CommandContext(ctx, "container", "exec", "-i", name,
		"socat", "-", fmt.Sprintf("TCP:127.0.0.1:%d", port))
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &pipeConn{stdin: stdin, stdout: stdout, cmd: cmd}, nil
}

const portalHTML = `<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no">
<title>Agent VM — Portal</title>
<style>
  *{margin:0;padding:0;box-sizing:border-box}
  body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',sans-serif;background:#0f0f23;color:#e0e0e0;min-height:100vh;display:flex;align-items:center;justify-content:center}
  .wrap{max-width:720px;width:100%;padding:2rem}
  h1{margin-bottom:.4rem;font-size:1.6rem;color:#fff}
  .sub{color:#667;margin-bottom:1.5rem;font-size:.9rem}
  .card{background:#1a1a2e;border:1px solid #2a2a4a;border-radius:10px;padding:1.2rem 1.4rem;margin-bottom:.8rem;display:flex;align-items:center;justify-content:space-between}
  .card:hover{border-color:#3a3a6a}
  .info{flex:1;min-width:0}
  .name{font-size:1.1rem;font-weight:600;color:#fff;margin-bottom:.2rem}
  .meta{font-size:.8rem;color:#667;display:flex;gap:1rem;flex-wrap:wrap}
  .dot{display:inline-block;width:7px;height:7px;border-radius:50%;margin-right:4px;vertical-align:middle}
  .on{background:#00d68f}.off{background:#555}.red{background:#ff4d6d}
  .actions{display:flex;gap:.4rem;flex-shrink:0}
  .btn{padding:.45rem 1rem;border-radius:7px;border:none;cursor:pointer;font-size:.82rem;font-weight:500;transition:opacity .15s;white-space:nowrap}
  .btn:hover{opacity:.8}
  .btn-go{background:#4a6cf7;color:#fff}
  .btn-alt{background:#2a2a4a;color:#aab;border:1px solid #3a3a5a}
  .btn:disabled{opacity:.4;cursor:wait}
  .empty{text-align:center;padding:3rem;color:#555}
  code{background:#1a1a2e;padding:.2rem .4rem;border-radius:4px;font-size:.85rem;color:#7c7c9c}
</style>
</head>
<body>
<div class="wrap">
  <h1>Agent VM</h1>
  <div class="sub">Development container portal</div>
  <div id="list"></div>
</div>
<script>
async function refresh(){
  try{
    const r=await fetch('/api/containers');
    const vms=await r.json();
    const el=document.getElementById('list');
    if(!vms||vms.length===0){
      el.innerHTML='<div class="empty">No containers yet.<br><br>Start one: <code>agent-vm start</code></div>';
      return;
    }
    el.innerHTML=vms.map(function(v){
      var h='<div class="card"><div class="info">'+
        '<div class="name">'+v.name+'</div><div class="meta">'+
        '<span><span class="dot '+(v.running?'on':'red')+'"></span>'+(v.running?'Running':'Stopped')+'</span>';
      if(v.running){
        h+='<span><span class="dot '+(v.web_running?'on':'off')+'"></span>OpenCode</span>';
        h+='<span><span class="dot '+(v.term_running?'on':'off')+'"></span>Terminal</span>';
      }
      h+='</div></div>';
      if(v.running){
        h+='<div class="actions">';
        h+='<button class="btn btn-go" onclick="openVM(\''+v.name+'\',\'web\',this)">OpenCode</button>';
        h+='<button class="btn btn-alt" onclick="openVM(\''+v.name+'\',\'terminal\',this)">Terminal</button>';
        h+='</div>';
      }
      h+='</div>';
      return h;
    }).join('');
  }catch(e){console.error(e)}
}
async function openVM(name,type,btn){
  var label=type==='terminal'?'Terminal':'OpenCode';
  btn.disabled=true;btn.textContent='Starting...';
  try{
    var action=type==='terminal'?'start-terminal':'start-web';
    var r=await fetch('/api/containers/'+name+'/'+action,{method:'POST'});
    if(!r.ok){alert(await r.text());return}
    var p=window.location.port?(':'+window.location.port):'';
    var suffix=type==='terminal'?'-term':'';
    window.open('http://'+name+suffix+'.localhost'+p+'/',name+'-'+type);
  }catch(e){alert(e)}
  btn.disabled=false;btn.textContent=label;
}
refresh();setInterval(refresh,3000);
</script>
</body>
</html>`
