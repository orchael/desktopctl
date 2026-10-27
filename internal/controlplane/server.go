package controlplane

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"github.com/orchael/desktopctl/internal/desktop"
	"github.com/orchael/desktopctl/internal/store"
)

type Server struct {
	service    *Service
	logger     *slog.Logger
	static     http.Handler
	apiToken   string
	standalone bool
	jobs       *JobManager
	accessURL  func(context.Context, *store.Desktop, string) (string, error)
}

func (s *Server) WithJobRunner(runner JobRunner) *Server {
	s.jobs = NewJobManager(runner)
	return s
}

func NewServer(service *Service, staticDir string, logger *slog.Logger, apiToken string) *Server {
	var static http.Handler
	if staticDir != "" {
		static = http.FileServer(http.Dir(staticDir))
	}
	return &Server{service: service, logger: logger, static: static, apiToken: apiToken, standalone: service.standalone, accessURL: fetchDesktopAccessURL}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /api/desktops", s.listDesktops)
	mux.HandleFunc("POST /api/desktops", s.createDesktop)
	mux.HandleFunc("GET /api/jobs/{id}", s.getJob)
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
	if !s.requireMutationAuth(w, r) {
		return
	}
	organizationID, ok := s.requireOrganization(w, r)
	if !ok {
		return
	}
	includeTerminated := r.URL.Query().Get("all") == "true"
	desktops, err := s.service.ListDesktops(r.Context(), organizationID, includeTerminated)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, desktops)
}

func (s *Server) createDesktop(w http.ResponseWriter, r *http.Request) {
	if s.standalone {
		if !s.requireMutationAuth(w, r) {
			return
		}
		if s.jobs == nil {
			writeError(w, http.StatusServiceUnavailable, errors.New("lifecycle runner is unavailable"))
			return
		}
		var req CreateDesktopRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, errors.New("invalid create request"))
			return
		}
		args, err := createCLIArgs(&req, s.service.cfg.Environment())
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		id, err := desktop.GenerateID()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		args = append([]string{"create", "--desktop-id", id}, args...)
		s.enqueueJob(w, "create", id, args...)
		return
	}
	if !s.requireMutationAuth(w, r) {
		return
	}
	organizationID, ok := s.requireOrganization(w, r)
	if !ok {
		return
	}
	var req CreateDesktopRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("decode request: %w", err))
		return
	}
	result, err := s.service.CreateDesktop(r.Context(), organizationID, &req)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
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
	if len(parts) == 2 && parts[1] == "access" && r.Method == http.MethodGet {
		s.getDesktopAccess(w, r, id)
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
		s.terminateDesktop(w, r, id)
	default:
		writeError(w, http.StatusNotFound, errors.New("unknown action"))
	}
}

func (s *Server) getDesktopAccess(w http.ResponseWriter, r *http.Request, id string) {
	if !s.standalone {
		writeError(w, http.StatusNotFound, errors.New("unknown endpoint"))
		return
	}
	if !s.requireMutationAuth(w, r) {
		return
	}
	desktop, err := s.service.GetDesktop(r.Context(), "", id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if desktop.State != store.StateReady && desktop.State != store.StateUnhealthy {
		writeError(w, http.StatusConflict, errors.New("desktop is not running"))
		return
	}
	link, err := s.accessURL(r.Context(), desktop.Desktop, s.service.cfg.Desktop.SSHKeyPath)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("could not create desktop access link"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Location", link)
	w.WriteHeader(http.StatusSeeOther)
}

func (s *Server) getDesktop(w http.ResponseWriter, r *http.Request, id string) {
	if !s.requireMutationAuth(w, r) {
		return
	}
	organizationID, ok := s.requireOrganization(w, r)
	if !ok {
		return
	}
	desktop, err := s.service.GetDesktop(r.Context(), organizationID, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, desktop)
}

func (s *Server) refreshDesktop(w http.ResponseWriter, r *http.Request, id string) {
	if !s.requireMutationAuth(w, r) {
		return
	}
	organizationID, ok := s.requireOrganization(w, r)
	if !ok {
		return
	}
	desktop, err := s.service.RefreshDesktop(r.Context(), organizationID, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, desktop)
}

func (s *Server) startDesktop(w http.ResponseWriter, r *http.Request, id string) {
	if s.standalone {
		if !s.requireMutationAuth(w, r) {
			return
		}
		s.enqueueJob(w, "start", id, "start", id)
		return
	}
	if !s.requireMutationAuth(w, r) {
		return
	}
	organizationID, ok := s.requireOrganization(w, r)
	if !ok {
		return
	}
	result, err := s.service.StartDesktop(r.Context(), organizationID, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) stopDesktop(w http.ResponseWriter, r *http.Request, id string) {
	if s.standalone {
		if !s.requireMutationAuth(w, r) {
			return
		}
		s.enqueueJob(w, "stop", id, "stop", id)
		return
	}
	if !s.requireMutationAuth(w, r) {
		return
	}
	organizationID, ok := s.requireOrganization(w, r)
	if !ok {
		return
	}
	result, err := s.service.StopDesktop(r.Context(), organizationID, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) terminateDesktop(w http.ResponseWriter, r *http.Request, id string) {
	if s.standalone {
		if !s.requireMutationAuth(w, r) {
			return
		}
		var body struct {
			Confirm string `json:"confirm"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&body); err != nil || body.Confirm != id {
			writeError(w, http.StatusBadRequest, errors.New("confirm must match the desktop ID"))
			return
		}
		s.enqueueJob(w, "terminate", id, "terminate", id)
		return
	}
	if !s.requireMutationAuth(w, r) {
		return
	}
	organizationID, ok := s.requireOrganization(w, r)
	if !ok {
		return
	}
	result, err := s.service.TerminateDesktop(r.Context(), organizationID, id)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (s *Server) enqueueJob(w http.ResponseWriter, action, id string, args ...string) {
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("lifecycle runner is unavailable"))
		return
	}
	job, err := s.jobs.Enqueue(action, id, args...)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrJobQueueFull) || errors.Is(err, ErrJobAlreadyActive) {
			status = http.StatusConflict
		}
		writeError(w, status, err)
		return
	}
	writeJSON(w, http.StatusAccepted, job)
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	if !s.requireMutationAuth(w, r) {
		return
	}
	if s.jobs == nil {
		writeError(w, http.StatusServiceUnavailable, errors.New("lifecycle runner is unavailable"))
		return
	}
	job, ok := s.jobs.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, errors.New("job not found"))
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) requireOrganization(w http.ResponseWriter, r *http.Request) (string, bool) {
	if s.standalone {
		return "", true
	}
	organizationID := strings.TrimSpace(r.Header.Get("X-Organization-ID"))
	if organizationID == "" {
		writeError(w, http.StatusBadRequest, errors.New("X-Organization-ID is required"))
		return "", false
	}
	return organizationID, true
}

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
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

func (s *Server) requireMutationAuth(w http.ResponseWriter, r *http.Request) bool {
	if s.apiToken == "" {
		return true
	}
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, prefix) {
		writeError(w, http.StatusUnauthorized, errors.New("missing or invalid bearer token"))
		return false
	}
	token := strings.TrimPrefix(header, prefix)
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.apiToken)) != 1 {
		writeError(w, http.StatusUnauthorized, errors.New("missing or invalid bearer token"))
		return false
	}
	return true
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
