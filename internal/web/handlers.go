package web

import (
	"encoding/json"
	"net/http"
	"strings"
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
