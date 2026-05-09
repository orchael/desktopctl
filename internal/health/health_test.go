package health

import (
	"context"
	"errors"
	"testing"
)

func TestBuildReport_allPass(t *testing.T) {
	results := []CheckResult{
		{Name: "ssh", Status: StatusPass},
		{Name: "novnc", Status: StatusPass},
	}
	r := buildReport("d-001", results)
	if !r.Passed {
		t.Error("expected passed=true")
	}
	if r.Summary != "all checks passed" {
		t.Errorf("summary: got %q", r.Summary)
	}
}

func TestBuildReport_withFailures(t *testing.T) {
	results := []CheckResult{
		{Name: "ssh", Status: StatusPass},
		{Name: "novnc", Status: StatusFail, Message: "timeout"},
		{Name: "docker", Status: StatusFail, Message: "not active"},
	}
	r := buildReport("d-002", results)
	if r.Passed {
		t.Error("expected passed=false")
	}
	if r.Summary == "all checks passed" {
		t.Error("summary should not say all passed")
	}
}

func TestFnChecker_pass(t *testing.T) {
	c := NewFnChecker("test-pass", func(ctx context.Context) error {
		return nil
	})
	result := c.Run(context.Background())
	if result.Status != StatusPass {
		t.Errorf("status: got %q", result.Status)
	}
}

func TestFnChecker_fail(t *testing.T) {
	c := NewFnChecker("test-fail", func(ctx context.Context) error {
		return errors.New("something broke")
	})
	result := c.Run(context.Background())
	if result.Status != StatusFail {
		t.Errorf("status: got %q", result.Status)
	}
	if result.Message != "something broke" {
		t.Errorf("message: got %q", result.Message)
	}
}

func TestRunner(t *testing.T) {
	pass := NewFnChecker("pass", func(ctx context.Context) error { return nil })
	fail := NewFnChecker("fail", func(ctx context.Context) error { return errors.New("bad") })

	r := NewRunner("d-test", pass, fail)
	report := r.Run(context.Background())

	if report.Passed {
		t.Error("expected report to not be passed")
	}
	if len(report.Checks) != 2 {
		t.Errorf("got %d checks", len(report.Checks))
	}
}

func TestStandardCheckers(t *testing.T) {
	checkers := StandardCheckers("d-001.desktops.orchael.dev", 22, 9445)
	if len(checkers) == 0 {
		t.Error("expected at least one standard checker")
	}
	for _, c := range checkers {
		if c.Name() == "" {
			t.Error("checker must have a name")
		}
	}
}
