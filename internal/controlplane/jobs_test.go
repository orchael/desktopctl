package controlplane

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/orchael/desktopctl/internal/store"
)

type jobRunnerFunc func(context.Context, ...string) error

func (f jobRunnerFunc) Run(ctx context.Context, args ...string) error { return f(ctx, args...) }

func TestCreateCLIArgsUsesOperatorLifecycleFlags(t *testing.T) {
	args, err := createCLIArgs(&CreateDesktopRequest{
		Owner: "orchael", Name: "work", Repos: []string{"orchael/desktopctl"},
		InstanceType: "m7i.xlarge", MarketType: "spot", SpotMaxPrice: "0.50",
	}, "dev")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(args, " ")
	for _, flag := range []string{"--github-owner orchael", "--name work", "--repo orchael/desktopctl", "--instance-type m7i.xlarge", "--spot --spot-max-price 0.50"} {
		if !strings.Contains(got, flag) {
			t.Errorf("args %q missing %q", got, flag)
		}
	}
	for _, req := range []CreateDesktopRequest{{}, {Owner: "x", Environment: "prod"}, {Owner: "x", UserDataBase64: "secret"}} {
		if _, err := createCLIArgs(&req, "dev"); err == nil {
			t.Errorf("accepted invalid request: %+v", req)
		}
	}
}

func TestStandaloneLifecycleQueuesCLIAndReportsResult(t *testing.T) {
	t.Parallel()
	called := make(chan []string, 1)
	runner := jobRunnerFunc(func(_ context.Context, args ...string) error {
		called <- args
		return nil
	})
	service := newTestService(testConfig(), store.NewInMemoryStore()).WithStandalone()
	server := NewServer(service, "", slog.Default(), "secret").WithJobRunner(runner).Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/desktops", strings.NewReader(`{"owner":"orchael","name":"work"}`))
	req.Header.Set("Authorization", "Bearer secret")
	req.Header.Set("X-Idempotency-Key", "11111111-1111-4111-8111-111111111111")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var job Job
	if err := json.NewDecoder(rec.Body).Decode(&job); err != nil {
		t.Fatal(err)
	}
	if job.Action != "create" || !strings.HasPrefix(job.DesktopID, "d-") {
		t.Fatalf("job = %+v", job)
	}
	retry := httptest.NewRequest(http.MethodPost, "/api/desktops", strings.NewReader(`{"owner":"orchael","name":"work"}`))
	retry.Header.Set("Authorization", "Bearer secret")
	retry.Header.Set("X-Idempotency-Key", "11111111-1111-4111-8111-111111111111")
	retryResponse := httptest.NewRecorder()
	server.ServeHTTP(retryResponse, retry)
	var sameJob Job
	if retryResponse.Code != http.StatusAccepted || json.NewDecoder(retryResponse.Body).Decode(&sameJob) != nil || sameJob.ID != job.ID || sameJob.DesktopID != job.DesktopID {
		t.Fatalf("retry created a second job: %d %s", retryResponse.Code, retryResponse.Body.String())
	}
	changed := httptest.NewRequest(http.MethodPost, "/api/desktops", strings.NewReader(`{"owner":"orchael","name":"different"}`))
	changed.Header.Set("Authorization", "Bearer secret")
	changed.Header.Set("X-Idempotency-Key", "11111111-1111-4111-8111-111111111111")
	changedResponse := httptest.NewRecorder()
	server.ServeHTTP(changedResponse, changed)
	if changedResponse.Code != http.StatusConflict {
		t.Fatalf("changed retry status = %d", changedResponse.Code)
	}
	select {
	case args := <-called:
		if len(args) < 5 || args[0] != "create" || args[1] != "--desktop-id" || args[2] != job.DesktopID {
			t.Fatalf("CLI args = %v", args)
		}
	case <-time.After(time.Second):
		t.Fatal("CLI runner was not called")
	}
	select {
	case <-called:
		t.Fatal("retry ran a second CLI operation")
	default:
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		get := httptest.NewRequest(http.MethodGet, "/api/jobs/"+job.ID, nil)
		get.Header.Set("Authorization", "Bearer secret")
		status := httptest.NewRecorder()
		server.ServeHTTP(status, get)
		if status.Code != http.StatusOK {
			t.Fatalf("job status: %d", status.Code)
		}
		var updated Job
		if err := json.NewDecoder(status.Body).Decode(&updated); err != nil {
			t.Fatal(err)
		}
		if updated.State == "succeeded" {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("job did not succeed")
}

func TestJobManagerHidesRunnerErrors(t *testing.T) {
	m := NewJobManager(jobRunnerFunc(func(context.Context, ...string) error { return errors.New("secret-value") }))
	job, err := m.Enqueue("stop", "d-test001", "stop", "d-test001")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		current, _ := m.Get(job.ID)
		if current.State == "failed" {
			if strings.Contains(current.Error, "secret-value") {
				t.Fatal("runner error leaked")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("job did not fail")
}

func TestBoundedCLIOutputKeepsSafeDiagnostics(t *testing.T) {
	output := &boundedCLIOutput{}
	_, _ = output.Write([]byte("no matching AMI in region\npassword=do-not-log\nrepository preflight failed\n"))
	got := output.diagnostic()
	if !strings.Contains(got, "no matching AMI") || !strings.Contains(got, "repository preflight failed") || strings.Contains(got, "do-not-log") {
		t.Fatalf("unexpected diagnostic %q", got)
	}
}

func TestTerminateRequiresMatchingConfirmation(t *testing.T) {
	service := newTestService(testConfig(), store.NewInMemoryStore()).WithStandalone()
	server := NewServer(service, "", slog.Default(), "secret").WithJobRunner(jobRunnerFunc(func(context.Context, ...string) error { return nil })).Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/desktops/d-test001/terminate", strings.NewReader(`{"confirm":"other"}`))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
}
