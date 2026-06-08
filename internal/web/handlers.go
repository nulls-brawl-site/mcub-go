package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"nhooyr.io/websocket"
	"nhooyr.io/websocket/wsjson"
)

// ─────────────────────────────────────────────────────────────────────────────
// Standard API response envelope
// ─────────────────────────────────────────────────────────────────────────────

// APIResponse is the standard JSON response format for every API endpoint.
type APIResponse struct {
	OK     bool        `json:"ok"`
	Result interface{} `json:"result,omitempty"`
	Error  string      `json:"error,omitempty"`
}

// ─────────────────────────────────────────────────────────────────────────────
// JSON helpers
// ─────────────────────────────────────────────────────────────────────────────

// writeJSON serialises v as JSON and writes it with the given HTTP status code.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// writeError writes a standard error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, APIResponse{OK: false, Error: msg})
}

// writeOK writes a standard success response.
func writeOK(w http.ResponseWriter, result interface{}) {
	writeJSON(w, http.StatusOK, APIResponse{OK: true, Result: result})
}

// ─────────────────────────────────────────────────────────────────────────────
// KernelAPI — optional interface the kernel exposes for the web panel.
// ─────────────────────────────────────────────────────────────────────────────

// KernelAPI is the interface the kernel may implement to be controllable via
// the web panel.  Methods mirror those exposed by the Python web routes.
type KernelAPI interface {
	Status() string
	ListModules() []string
	LoadModule(path string) error
	UnloadModule(name string) error
	GetAllConfig() map[string]interface{}
	SetConfig(key, value string) error
	RecentLog(n int) []string
	Restart() error
	DispatchCommand(cmd string) (string, error)
	// Extended API
	GetAliases() map[string]string
	AddAlias(alias, target string) error
	RemoveAlias(alias string) error
	GetRepos() []string
	AddRepo(url string) error
	RemoveRepo(index int) error
	GetConfig() interface{}
	UpdateConfig(updates map[string]interface{}) error
	GetAdminID() int64
	GetLanguage() string
}

// ─────────────────────────────────────────────────────────────────────────────
// Sub-interfaces for optional kernel capabilities (used by new handlers so
// they work even when the kernel does not satisfy the full KernelAPI).
// ─────────────────────────────────────────────────────────────────────────────

type kernelInfoer interface {
	Status() string
	Uptime() time.Duration
	GetAdminID() int64
	GetLanguage() string
}

type kernelModuleLister interface {
	ListModules() []string
}

type kernelSystemModuler interface {
	ListSystemModules() []string
}

type kernelUserModuler interface {
	ListUserModules() []string
}

type kernelModuleReloader interface {
	ReloadModule(name string) error
}

type kernelConfiger interface {
	GetConfig() interface{}
	UpdateConfig(updates map[string]interface{}) error
}

type kernelAliaser interface {
	GetAliases() map[string]string
	AddAlias(alias, target string) error
	RemoveAlias(alias string) error
}

type kernelReporer interface {
	GetRepos() []string
	AddRepo(url string) error
	RemoveRepo(index int) error
}

type kernelLogger interface {
	RecentLog(n int) []string
}

// ─────────────────────────────────────────────────────────────────────────────
// Handler implementations
// ─────────────────────────────────────────────────────────────────────────────

// handleStatus — GET /api/status
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ka, ok := s.kernel.(KernelAPI)
	if !ok {
		writeOK(w, map[string]string{"status": "running"})
		return
	}
	writeOK(w, map[string]string{"status": ka.Status()})
}

// handleModules — GET /api/modules
func (s *Server) handleModules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ka, ok := s.kernel.(KernelAPI)
	if !ok {
		writeOK(w, []string{})
		return
	}
	writeOK(w, ka.ListModules())
}

// handleLoadModule — POST /api/modules/load
func (s *Server) handleLoadModule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ka, ok := s.kernel.(KernelAPI)
	if !ok {
		writeError(w, http.StatusNotImplemented, "kernel does not support module loading")
		return
	}
	var body struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	if err := ka.LoadModule(body.Path); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w, map[string]string{"loaded": body.Path})
}

// handleUnloadModule — POST /api/modules/unload
func (s *Server) handleUnloadModule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ka, ok := s.kernel.(KernelAPI)
	if !ok {
		writeError(w, http.StatusNotImplemented, "kernel does not support module unloading")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := ka.UnloadModule(body.Name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w, map[string]string{"unloaded": body.Name})
}

// handleConfig — GET /api/config  or  POST /api/config
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	ka, ok := s.kernel.(KernelAPI)
	if !ok {
		writeError(w, http.StatusNotImplemented, "kernel does not expose config")
		return
	}
	switch r.Method {
	case http.MethodGet:
		writeOK(w, ka.GetAllConfig())
	case http.MethodPost:
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		for k, v := range body {
			if err := ka.SetConfig(k, v); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		writeOK(w, map[string]bool{"saved": true})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleLog — GET /api/log?n=50
func (s *Server) handleLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ka, ok := s.kernel.(KernelAPI)
	if !ok {
		writeOK(w, []string{})
		return
	}
	n := 50
	if raw := r.URL.Query().Get("n"); raw != "" {
		var parsed int
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil && parsed > 0 {
			n = parsed
		}
	}
	writeOK(w, ka.RecentLog(n))
}

// handleRestart — POST /api/restart
func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ka, ok := s.kernel.(KernelAPI)
	if !ok {
		writeError(w, http.StatusNotImplemented, "kernel does not support restart")
		return
	}
	if err := ka.Restart(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w, map[string]string{"message": "restart scheduled"})
}

// handleCommand — POST /api/command
func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ka, ok := s.kernel.(KernelAPI)
	if !ok {
		writeError(w, http.StatusNotImplemented, "kernel does not support command dispatch")
		return
	}
	var body struct {
		Command string `json:"command"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Command) == "" {
		writeError(w, http.StatusBadRequest, "command is required")
		return
	}
	result, err := ka.DispatchCommand(body.Command)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w, map[string]string{"result": result})
}

// handleLogin — POST /api/auth/login
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	token, err := s.auth.Authenticate(body.Password)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	writeOK(w, map[string]string{"token": token})
}

// handleAuthStatus — GET /api/auth/status
func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	token := bearerToken(r)
	writeOK(w, map[string]bool{"authenticated": s.auth.Validate(token)})
}

// ─────────────────────────────────────────────────────────────────────────────
// Extended handlers
// ─────────────────────────────────────────────────────────────────────────────

// handleInfo — GET /api/info
func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	info := map[string]interface{}{
		"status":   "running",
		"uptime":   0,
		"admin_id": int64(0),
		"language": "en",
	}
	if ki, ok := s.kernel.(kernelInfoer); ok {
		info["status"] = ki.Status()
		info["uptime"] = ki.Uptime().Seconds()
		info["admin_id"] = ki.GetAdminID()
		info["language"] = ki.GetLanguage()
	}
	writeOK(w, info)
}

// handleSystemModules — GET /api/modules/system
func (s *Server) handleSystemModules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if km, ok := s.kernel.(kernelSystemModuler); ok {
		writeOK(w, km.ListSystemModules())
		return
	}
	writeOK(w, []string{})
}

// handleUserModules — GET /api/modules/user
func (s *Server) handleUserModules(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if km, ok := s.kernel.(kernelUserModuler); ok {
		writeOK(w, km.ListUserModules())
		return
	}
	writeOK(w, []string{})
}

// handleReloadModule — POST /api/modules/reload
func (s *Server) handleReloadModule(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	km, ok := s.kernel.(kernelModuleReloader)
	if !ok {
		writeError(w, http.StatusNotImplemented, "kernel does not support module reload")
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := km.ReloadModule(body.Name); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w, map[string]string{"reloaded": body.Name})
}

// handleGetConfig — GET /api/config (masked)
func (s *Server) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if kc, ok := s.kernel.(kernelConfiger); ok {
		writeOK(w, kc.GetConfig())
		return
	}
	// Fall back to old interface.
	if ka, ok := s.kernel.(KernelAPI); ok {
		writeOK(w, ka.GetAllConfig())
		return
	}
	writeOK(w, map[string]interface{}{})
}

// handleUpdateConfig — PATCH /api/config
func (s *Server) handleUpdateConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	kc, ok := s.kernel.(kernelConfiger)
	if !ok {
		writeError(w, http.StatusNotImplemented, "kernel does not support config update")
		return
	}
	var updates map[string]interface{}
	if err := json.NewDecoder(r.Body).Decode(&updates); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := kc.UpdateConfig(updates); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w, map[string]bool{"updated": true})
}

// handleGetAliases — GET /api/aliases
func (s *Server) handleGetAliases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if ka, ok := s.kernel.(kernelAliaser); ok {
		writeOK(w, ka.GetAliases())
		return
	}
	writeOK(w, map[string]string{})
}

// handleAddAlias — POST /api/aliases
func (s *Server) handleAddAlias(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ka, ok := s.kernel.(kernelAliaser)
	if !ok {
		writeError(w, http.StatusNotImplemented, "kernel does not support aliases")
		return
	}
	var body struct {
		Alias  string `json:"alias"`
		Target string `json:"target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Alias == "" || body.Target == "" {
		writeError(w, http.StatusBadRequest, "alias and target are required")
		return
	}
	if err := ka.AddAlias(body.Alias, body.Target); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w, map[string]string{"alias": body.Alias, "target": body.Target})
}

// handleDeleteAlias — DELETE /api/aliases/:alias
func (s *Server) handleDeleteAlias(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ka, ok := s.kernel.(kernelAliaser)
	if !ok {
		writeError(w, http.StatusNotImplemented, "kernel does not support aliases")
		return
	}
	// Extract alias name from the URL path: /api/aliases/<alias>
	alias := strings.TrimPrefix(r.URL.Path, "/api/aliases/")
	alias = strings.TrimSpace(alias)
	if alias == "" {
		writeError(w, http.StatusBadRequest, "alias name is required in path")
		return
	}
	if err := ka.RemoveAlias(alias); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w, map[string]string{"deleted": alias})
}

// handleGetRepos — GET /api/repos
func (s *Server) handleGetRepos(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if kr, ok := s.kernel.(kernelReporer); ok {
		writeOK(w, kr.GetRepos())
		return
	}
	writeOK(w, []string{})
}

// handleAddRepo — POST /api/repos
func (s *Server) handleAddRepo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	kr, ok := s.kernel.(kernelReporer)
	if !ok {
		writeError(w, http.StatusNotImplemented, "kernel does not support repos")
		return
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.URL == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	if err := kr.AddRepo(body.URL); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w, map[string]string{"added": body.URL})
}

// handleDeleteRepo — DELETE /api/repos/:id
func (s *Server) handleDeleteRepo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	kr, ok := s.kernel.(kernelReporer)
	if !ok {
		writeError(w, http.StatusNotImplemented, "kernel does not support repos")
		return
	}
	// Extract index from the URL path: /api/repos/<id>
	idStr := strings.TrimPrefix(r.URL.Path, "/api/repos/")
	idStr = strings.TrimSpace(idStr)
	id, err := strconv.Atoi(idStr)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid repo id")
		return
	}
	if err := kr.RemoveRepo(id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeOK(w, map[string]int{"deleted": id})
}

// handleAliasesRouter dispatches alias CRUD requests.
func (s *Server) handleAliasesRouter(w http.ResponseWriter, r *http.Request) {
	// /api/aliases        GET → list, POST → add
	// /api/aliases/<name> DELETE → remove
	path := strings.TrimSuffix(r.URL.Path, "/")
	isRoot := path == "/api/aliases"
	switch {
	case r.Method == http.MethodGet && isRoot:
		s.handleGetAliases(w, r)
	case r.Method == http.MethodPost && isRoot:
		s.handleAddAlias(w, r)
	case r.Method == http.MethodDelete && !isRoot:
		s.handleDeleteAlias(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleReposRouter dispatches repo CRUD requests.
func (s *Server) handleReposRouter(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	isRoot := path == "/api/repos"
	switch {
	case r.Method == http.MethodGet && isRoot:
		s.handleGetRepos(w, r)
	case r.Method == http.MethodPost && isRoot:
		s.handleAddRepo(w, r)
	case r.Method == http.MethodDelete && !isRoot:
		s.handleDeleteRepo(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleLogWS — WebSocket /ws/log — streams log lines in real time.
func (s *Server) handleLogWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // allow any origin for the panel
	})
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "done")

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()

	kl, ok := s.kernel.(kernelLogger)
	if !ok {
		// No log source — send empty marker and close.
		_ = wsjson.Write(ctx, conn, []string{})
		return
	}

	// Send the recent 100 log lines immediately.
	lines := kl.RecentLog(100)
	if err := wsjson.Write(ctx, conn, lines); err != nil {
		return
	}

	// Poll for new log lines every second until the client disconnects.
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	last := len(lines)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			all := kl.RecentLog(500)
			if len(all) > last {
				newLines := all[last:]
				last = len(all)
				if err := wsjson.Write(ctx, conn, newLines); err != nil {
					return
				}
			}
		}
	}
}
