// Package web provides a lightweight HTTP control panel for the MCUB kernel,
// porting core/web/app.py + routes.py to Go's standard net/http library.
package web

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// ─────────────────────────────────────────────────────────────────────────────
// Server
// ─────────────────────────────────────────────────────────────────────────────

// Server is the MCUB web-panel HTTP server.
type Server struct {
	kernel interface{}
	mux    *http.ServeMux
	server *http.Server
	auth   *AuthManager
	mu     sync.RWMutex
}

// New creates a new web panel Server.
//
//   - kernel: the running MCUB kernel (may implement KernelAPI for richer
//     functionality; can be nil for testing).
//   - host/port: bind address.
//   - password: plaintext password for the web panel.  Pass an empty string to
//     disable authentication.
func New(kernel interface{}, host string, port int, password string) *Server {
	s := &Server{
		kernel: kernel,
		mux:    http.NewServeMux(),
		auth:   NewAuthManager(password),
	}

	s.server = &http.Server{
		Addr:         fmt.Sprintf("%s:%d", host, port),
		Handler:      s.mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	s.registerRoutes()
	return s
}

// Start starts the HTTP server in the background.  The server runs until Stop
// is called or the returned channel is closed.
func (s *Server) Start(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return s.Stop(context.Background())
	}
}

// Stop gracefully shuts down the HTTP server within the timeout embedded in ctx.
func (s *Server) Stop(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}

// Addr returns the bound address (e.g. "127.0.0.1:8080").
func (s *Server) Addr() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.server.Addr
}

// ─────────────────────────────────────────────────────────────────────────────
// Route registration
// ─────────────────────────────────────────────────────────────────────────────

func (s *Server) registerRoutes() {
	// Public endpoints (no auth required)
	s.mux.HandleFunc("/api/auth/login", s.handleLogin)
	s.mux.HandleFunc("/api/auth/status", s.handleAuthStatus)

	// Protected endpoints — wrap with auth middleware when a password is set.
	protected := []struct {
		pattern string
		handler http.HandlerFunc
	}{
		{"/api/status", s.handleStatus},
		{"/api/modules", s.handleModules},
		{"/api/modules/load", s.handleLoadModule},
		{"/api/modules/unload", s.handleUnloadModule},
		{"/api/config", s.handleConfig},
		{"/api/log", s.handleLog},
		{"/api/restart", s.handleRestart},
		{"/api/command", s.handleCommand},
	}

	for _, p := range protected {
		handler := http.Handler(p.handler)
		if s.auth.passwordHash != "" {
			handler = s.auth.AuthMiddleware(handler)
		}
		s.mux.Handle(p.pattern, handler)
	}
}
