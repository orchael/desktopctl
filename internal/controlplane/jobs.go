package controlplane

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const jobTimeout = 90 * time.Minute

var (
	ErrJobQueueFull        = errors.New("another fleet operation is queued; wait for it to finish")
	ErrJobAlreadyActive    = errors.New("a lifecycle operation is already active for this desktop")
	ErrIdempotencyConflict = errors.New("create request key was already used with different details")
)

type JobRunner interface {
	Run(context.Context, ...string) error
}

// CLIJobRunner uses the same desktopctl lifecycle commands as an operator.
// Output is discarded because CLI diagnostics may contain sensitive values.
type CLIJobRunner struct {
	Binary     string
	ConfigPath string
	SourceDir  string
	Profile    string
	Region     string
}

func (r CLIJobRunner) Run(ctx context.Context, args ...string) error {
	commandArgs := []string{"--config", r.ConfigPath}
	if r.Profile != "" {
		commandArgs = append(commandArgs, "--profile", r.Profile)
	}
	if r.Region != "" {
		commandArgs = append(commandArgs, "--region", r.Region)
	}
	commandArgs = append(commandArgs, args...)
	command := exec.CommandContext(ctx, r.Binary, commandArgs...)
	command.Dir = r.SourceDir
	configureCLIProcess(command)
	output := &boundedCLIOutput{}
	command.Stdout = output
	command.Stderr = output
	if err := command.Run(); err != nil {
		if diagnostic := output.diagnostic(); diagnostic != "" {
			return fmt.Errorf("desktopctl operation failed: %w; diagnostic: %s", err, diagnostic)
		}
		return fmt.Errorf("desktopctl operation failed: %w", err)
	}
	return nil
}

const maxCLIOutput = 8192

type boundedCLIOutput struct {
	mu   sync.Mutex
	data []byte
}

func (b *boundedCLIOutput) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(value) >= maxCLIOutput {
		b.data = append(b.data[:0], value[len(value)-maxCLIOutput:]...)
		return len(value), nil
	}
	b.data = append(b.data, value...)
	if len(b.data) > maxCLIOutput {
		b.data = append([]byte(nil), b.data[len(b.data)-maxCLIOutput:]...)
	}
	return len(value), nil
}

var sensitiveDiagnostic = regexp.MustCompile(`(?i)(token|secret|credential|password|passphrase|authorization|bearer|private[ _-]?key|access[ _-]?key)`)
var longDiagnosticValue = regexp.MustCompile(`[A-Za-z0-9_./+=-]{32,}`)

func (b *boundedCLIOutput) diagnostic() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	lines := strings.Split(string(b.data), "\n")
	clean := make([]string, 0, 3)
	for i := len(lines) - 1; i >= 0 && len(clean) < 3; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || sensitiveDiagnostic.MatchString(line) {
			continue
		}
		line = longDiagnosticValue.ReplaceAllString(line, "[redacted]")
		if len(line) > 240 {
			line = line[:240]
		}
		clean = append(clean, line)
	}
	for i, j := 0, len(clean)-1; i < j; i, j = i+1, j-1 {
		clean[i], clean[j] = clean[j], clean[i]
	}
	return strings.Join(clean, " | ")
}

type Job struct {
	ID        string    `json:"id"`
	Action    string    `json:"action"`
	DesktopID string    `json:"desktop_id"`
	State     string    `json:"state"`
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type JobManager struct {
	mu       sync.Mutex
	jobs     map[string]*Job
	requests map[string]createRequestEntry
	pending  int
	queue    chan jobRequest
	runner   JobRunner
}

type createRequestEntry struct {
	jobID  string
	digest [32]byte
}

type jobRequest struct {
	id   string
	args []string
}

func NewJobManager(runner JobRunner) *JobManager {
	m := &JobManager{jobs: make(map[string]*Job), requests: make(map[string]createRequestEntry), queue: make(chan jobRequest, 16), runner: runner}
	go m.work()
	return m
}

func (m *JobManager) Enqueue(action, desktopID string, args ...string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.enqueue("", [32]byte{}, action, desktopID, args...)
}

// EnqueueCreate returns the same job for an identical request key and body.
func (m *JobManager) EnqueueCreate(key string, digest [32]byte, desktopID string, args ...string) (Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.requests[key]; ok {
		if existing.digest != digest {
			return Job{}, ErrIdempotencyConflict
		}
		if job, ok := m.jobs[existing.jobID]; ok {
			return *job, nil
		}
		return Job{}, ErrIdempotencyConflict
	}
	return m.enqueue(key, digest, "create", desktopID, args...)
}

func (m *JobManager) enqueue(key string, digest [32]byte, action, desktopID string, args ...string) (Job, error) {
	if m.pending >= cap(m.queue) {
		return Job{}, ErrJobQueueFull
	}
	for _, existing := range m.jobs {
		if existing.DesktopID == desktopID && (existing.State == "queued" || existing.State == "running") {
			return Job{}, ErrJobAlreadyActive
		}
	}
	if len(m.jobs) >= 256 {
		var oldest *Job
		for _, existing := range m.jobs {
			if existing.State != "queued" && existing.State != "running" && (oldest == nil || existing.UpdatedAt.Before(oldest.UpdatedAt)) {
				oldest = existing
			}
		}
		if oldest != nil {
			delete(m.jobs, oldest.ID)
			for requestKey, entry := range m.requests {
				if entry.jobID == oldest.ID {
					delete(m.requests, requestKey)
				}
			}
		}
	}
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return Job{}, err
	}
	now := time.Now().UTC()
	job := Job{ID: hex.EncodeToString(raw), Action: action, DesktopID: desktopID, State: "queued", CreatedAt: now, UpdatedAt: now}
	m.jobs[job.ID] = &job
	if key != "" {
		m.requests[key] = createRequestEntry{jobID: job.ID, digest: digest}
	}
	m.pending++
	m.queue <- jobRequest{id: job.ID, args: args}
	return job, nil
}

func (m *JobManager) Get(id string) (Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	job, ok := m.jobs[id]
	if !ok {
		return Job{}, false
	}
	return *job, true
}

func (m *JobManager) work() {
	for request := range m.queue {
		m.mu.Lock()
		job := m.jobs[request.id]
		job.State = "running"
		job.UpdatedAt = time.Now().UTC()
		m.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
		err := m.runner.Run(ctx, request.args...)
		cancel()
		m.mu.Lock()
		if err != nil {
			slog.Error("lifecycle operation failed", "job_id", job.ID, "action", job.Action, "desktop_id", job.DesktopID, "error", err)
			job.State = "failed"
			job.Error = "Operation failed. Inspect the fleet record and server logs before retrying."
		} else {
			job.State = "succeeded"
		}
		job.UpdatedAt = time.Now().UTC()
		m.pending--
		m.mu.Unlock()
	}
}

func createCLIArgs(req *CreateDesktopRequest, environment string) ([]string, error) {
	owner := strings.TrimSpace(req.Owner)
	if owner == "" || len(owner) > 100 || strings.ContainsAny(owner, "\r\n\x00") {
		return nil, errors.New("a valid GitHub owner is required")
	}
	if req.Environment != "" && req.Environment != environment {
		return nil, errors.New("environment must match this fleet")
	}
	if req.UserDataBase64 != "" {
		return nil, errors.New("custom user data is not supported by the browser")
	}
	args := []string{"--github-owner", owner}
	if req.Name != "" {
		if len(req.Name) > 100 || strings.ContainsAny(req.Name, "\r\n\x00") {
			return nil, errors.New("invalid desktop name")
		}
		args = append(args, "--name", req.Name)
	}
	if len(req.Repos) > 20 {
		return nil, errors.New("too many repositories")
	}
	for _, repository := range req.Repos {
		if repository == "" || len(repository) > 300 || strings.ContainsAny(repository, "\r\n\x00") {
			return nil, errors.New("invalid repository")
		}
		args = append(args, "--repo", repository)
	}
	if req.AMIID != "" {
		args = append(args, "--ami", req.AMIID)
	}
	if req.InstanceType != "" {
		args = append(args, "--instance-type", req.InstanceType)
	}
	if req.VolumeSize < 0 {
		return nil, errors.New("volume size must be positive")
	}
	if req.VolumeSize > 0 {
		args = append(args, "--volume-size", strconv.Itoa(req.VolumeSize))
	}
	switch req.MarketType {
	case "", "on-demand":
		if req.SpotMaxPrice != "" {
			return nil, errors.New("spot max price requires spot market type")
		}
	case "spot":
		args = append(args, "--spot")
		if req.SpotMaxPrice != "" {
			args = append(args, "--spot-max-price", req.SpotMaxPrice)
		}
	default:
		return nil, errors.New("market type must be on-demand or spot")
	}
	return args, nil
}
