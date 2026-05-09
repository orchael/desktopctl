package health

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// Status represents the outcome of a single health check.
type Status string

const (
	StatusPass    Status = "pass"
	StatusFail    Status = "fail"
	StatusSkipped Status = "skipped"
)

// CheckResult is the result of one named health check.
type CheckResult struct {
	Name    string `json:"name"`
	Status  Status `json:"status"`
	Message string `json:"message,omitempty"`
}

// Report collects results from all health checks.
type Report struct {
	DesktopID string        `json:"desktop_id"`
	Checks    []CheckResult `json:"checks"`
	Passed    bool          `json:"passed"`
	Summary   string        `json:"summary"`
}

// buildReport constructs a Report from a slice of results.
func buildReport(id string, results []CheckResult) *Report {
	passed := true
	failures := []string{}
	for _, r := range results {
		if r.Status == StatusFail {
			passed = false
			failures = append(failures, r.Name)
		}
	}
	summary := "all checks passed"
	if !passed {
		summary = fmt.Sprintf("failed: %s", strings.Join(failures, ", "))
	}
	return &Report{
		DesktopID: id,
		Checks:    results,
		Passed:    passed,
		Summary:   summary,
	}
}

// Checker is a single health check.
type Checker interface {
	Name() string
	Run(ctx context.Context) CheckResult
}

// Runner executes a list of Checkers and aggregates results.
type Runner struct {
	desktopID string
	checkers  []Checker
}

// NewRunner returns a Runner.
func NewRunner(desktopID string, checkers ...Checker) *Runner {
	return &Runner{desktopID: desktopID, checkers: checkers}
}

// Run executes all checks and returns the aggregated report.
func (r *Runner) Run(ctx context.Context) *Report {
	results := make([]CheckResult, 0, len(r.checkers))
	for _, c := range r.checkers {
		results = append(results, c.Run(ctx))
	}
	return buildReport(r.desktopID, results)
}

// --- built-in checkers ---

// TCPChecker verifies that a TCP port is reachable.
type TCPChecker struct {
	name    string
	address string // host:port
	timeout time.Duration
}

// NewTCPChecker creates a TCPChecker.
func NewTCPChecker(name, address string, timeout time.Duration) *TCPChecker {
	return &TCPChecker{name: name, address: address, timeout: timeout}
}

func (c *TCPChecker) Name() string { return c.name }

func (c *TCPChecker) Run(ctx context.Context) CheckResult {
	d := net.Dialer{Timeout: c.timeout}
	conn, err := d.DialContext(ctx, "tcp", c.address)
	if err != nil {
		return CheckResult{Name: c.name, Status: StatusFail, Message: err.Error()}
	}
	conn.Close()
	return CheckResult{Name: c.name, Status: StatusPass}
}

// HTTPSChecker verifies that an HTTPS endpoint returns a 2xx status.
type HTTPSChecker struct {
	name    string
	url     string
	timeout time.Duration
}

// NewHTTPSChecker creates an HTTPSChecker.
func NewHTTPSChecker(name, url string, timeout time.Duration) *HTTPSChecker {
	return &HTTPSChecker{name: name, url: url, timeout: timeout}
}

func (c *HTTPSChecker) Name() string { return c.name }

func (c *HTTPSChecker) Run(ctx context.Context) CheckResult {
	client := &http.Client{
		Timeout: c.timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
		},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return CheckResult{Name: c.name, Status: StatusFail, Message: err.Error()}
	}
	resp, err := client.Do(req)
	if err != nil {
		return CheckResult{Name: c.name, Status: StatusFail, Message: err.Error()}
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return CheckResult{Name: c.name, Status: StatusFail,
			Message: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}
	return CheckResult{Name: c.name, Status: StatusPass}
}

// FnChecker wraps an arbitrary function as a Checker.
type FnChecker struct {
	name string
	fn   func(ctx context.Context) error
}

// NewFnChecker creates a Checker from a function.
func NewFnChecker(name string, fn func(ctx context.Context) error) *FnChecker {
	return &FnChecker{name: name, fn: fn}
}

func (c *FnChecker) Name() string { return c.name }

func (c *FnChecker) Run(ctx context.Context) CheckResult {
	if err := c.fn(ctx); err != nil {
		return CheckResult{Name: c.name, Status: StatusFail, Message: err.Error()}
	}
	return CheckResult{Name: c.name, Status: StatusPass}
}

// StandardCheckers returns the set of checks run against a provisioned desktop.
func StandardCheckers(hostname string, sshPort, bridgePort int) []Checker {
	sshAddr := fmt.Sprintf("%s:%d", hostname, sshPort)
	noVNCURL := fmt.Sprintf("https://%s/novnc", hostname)

	return []Checker{
		NewTCPChecker("ssh-port", sshAddr, 10*time.Second),
		NewHTTPSChecker("novnc-https", noVNCURL, 15*time.Second),
	}
}
