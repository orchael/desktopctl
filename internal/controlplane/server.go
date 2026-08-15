package controlplane

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"github.com/orchael/ai-desktops/internal/store"
)

type Server struct {
	service *Service
	logger  *slog.Logger
	static  http.Handler
}

func NewServer(service *Service, staticDir string, logger *slog.Logger) *Server {
	var static http.Handler
	if staticDir != "" {
		static = http.FileServer(http.Dir(staticDir))
	}
	return &Server{service: service, logger: logger, static: static}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /api/desktops", s.listDesktops)
	mux.HandleFunc("POST /api/desktops", s.createDesktop)
	mux.HandleFunc("/api/desktops/", s.desktopAction)
	if s.static != nil {
		mux.HandleFunc("/", s.serveStatic)
	}
	return requestLog(s.logger, mux)
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ready := s.service.Ready(r.Context())
	status := http.StatusOK
	if !ready.OK {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, ready)
}

func (s *Server) listDesktops(w http.ResponseWriter, r *http.Request) {
	includeTerminated := r.URL.Query().Get("all") == "true"
	desktops, err := s.service.ListDesktops(r.Context(), includeTerminated)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, desktops)
}

func (s *Server) createDesktop(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, errors.New("this operation requires the shared Pulumi lifecycle service extraction"))
}

func (s *Server) desktopAction(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/desktops/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, errors.New("desktop id required"))
		return
	}
	id := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		s.getDesktop(w, r, id)
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost {
		writeError(w, http.StatusNotFound, errors.New("unknown endpoint"))
		return
	}
	switch parts[1] {
	case "refresh":
		s.refreshDesktop(w, r, id)
	case "start":
		s.startDesktop(w, r, id)
	case "stop":
		s.stopDesktop(w, r, id)
	case "terminate":
		writeError(w, http.StatusNotImplemented, errors.New("this operation requires the shared Pulumi lifecycle service extraction"))
	default:
		writeError(w, http.StatusNotFound, errors.New("unknown action"))
	}
}

func (s *Server) getDesktop(w http.ResponseWriter, r *http.Request, id string) {
	desktop, err := s.service.GetDesktop(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, desktop)
}

func (s *Server) refreshDesktop(w http.ResponseWriter, r *http.Request, id string) {
	desktop, err := s.service.RefreshDesktop(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, desktop)
}

func (s *Server) startDesktop(w http.ResponseWriter, r *http.Request, id string) {
	result, err := s.service.StartDesktop(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) stopDesktop(w http.ResponseWriter, r *http.Request, id string) {
	result, err := s.service.StopDesktop(r.Context(), id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, errors.New("unknown API endpoint"))
		return
	}
	cleaned := path.Clean(r.URL.Path)
	if cleaned == "." || cleaned == "/" {
		r.URL.Path = "/"
		s.static.ServeHTTP(w, r)
		return
	}
	s.static.ServeHTTP(w, r)
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeError(w, http.StatusInternalServerError, err)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func requestLog(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Info("request", "method", r.Method, "path", r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
