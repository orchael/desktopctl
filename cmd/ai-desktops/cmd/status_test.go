package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/orchael/ai-desktops/internal/store"
)

func TestParseNoVNCOutput(t *testing.T) {
	realOutput := `Desktop URL : https://example.com:8443/access?token=abc123
  Expires     : 2026-07-12T11:44:54Z

  Open the URL in your browser. It is valid for one session.
  Run 'novnc-desktop-url' again to generate a new link.`

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "real novnc-desktop-url output",
			input: realOutput,
			want:  "https://example.com:8443/access?token=abc123",
		},
		{
			name:  "url line with leading whitespace",
			input: "  Desktop URL : https://host/access?token=tok\n  Expires : 2026-01-01\n",
			want:  "https://host/access?token=tok",
		},
		{
			name:  "url line only",
			input: "Desktop URL : https://host:8443/access?token=tok",
			want:  "https://host:8443/access?token=tok",
		},
		{
			name:  "empty output",
			input: "",
			want:  "",
		},
		{
			name:  "no matching line",
			input: "some other output\nwithout the expected prefix\n",
			want:  "",
		},
		{
			name:  "windows line endings",
			input: "Desktop URL : https://host/access?token=tok\r\n  Expires : 2026-01-01\r\n",
			want:  "https://host/access?token=tok",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseNoVNCOutput(tt.input)
			if got != tt.want {
				t.Errorf("parseNoVNCOutput() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPrintDesktopStatus_basicFields(t *testing.T) {
	d := &store.Desktop{
		DesktopID:   "d-abc123",
		State:       store.StateReady,
		GitHubOwner: "acme",
		Hostname:    "d-abc123.desktops.example.com",
		NoVNCURL:    "https://d-abc123.desktops.example.com:8443/",
		SSHTarget:   "ubuntu@d-abc123.desktops.example.com",
		InstanceID:  "i-0abc123",
		StackName:   "desktop-d-abc123",
		Readiness:   "provisioned",
		CreatedAt:   "2026-07-12T00:00:00Z",
		UpdatedAt:   "2026-07-12T00:01:00Z",
	}

	var buf bytes.Buffer
	printDesktopStatus(&buf, d, "us-east-2", "")
	out := buf.String()

	checks := []struct{ label, want string }{
		{"Desktop ID", "Desktop ID   : d-abc123"},
		{"State", "State        : ready"},
		{"Owner", "Owner        : acme"},
		{"Region", "Region       : us-east-2"},
		{"Hostname", "Hostname     : d-abc123.desktops.example.com"},
		{"Desktop URL", "Desktop URL  : https://d-abc123.desktops.example.com:8443/"},
		{"SSH target", "SSH target   : ubuntu@d-abc123.desktops.example.com"},
		{"Instance ID", "Instance ID  : i-0abc123"},
		{"Pulumi stack", "Pulumi stack : desktop-d-abc123"},
		{"Readiness", "Readiness    : provisioned"},
		{"Created", "Created      : 2026-07-12T00:00:00Z"},
		{"Updated", "Updated      : 2026-07-12T00:01:00Z"},
	}
	for _, c := range checks {
		if !strings.Contains(out, c.want) {
			t.Errorf("%s: output does not contain %q\nfull output:\n%s", c.label, c.want, out)
		}
	}
}

func TestPrintDesktopStatus_noVNCURL(t *testing.T) {
	d := &store.Desktop{
		DesktopID: "d-abc123",
		State:     store.StateReady,
		NoVNCURL:  "https://d-abc123.desktops.example.com:8443/",
	}

	var buf bytes.Buffer
	liveURL := "https://d-abc123.desktops.example.com:8443/access?token=tok123"
	printDesktopStatus(&buf, d, "us-east-1", liveURL)
	out := buf.String()

	if !strings.Contains(out, "NoVNC URL    : "+liveURL) {
		t.Errorf("NoVNC URL line missing or incorrect\nfull output:\n%s", out)
	}
}

func TestPrintDesktopStatus_noVNCURLAbsentWhenEmpty(t *testing.T) {
	d := &store.Desktop{
		DesktopID: "d-abc123",
		State:     store.StateStopped,
		NoVNCURL:  "https://d-abc123.desktops.example.com:8443/",
	}

	var buf bytes.Buffer
	printDesktopStatus(&buf, d, "us-east-1", "")
	out := buf.String()

	if strings.Contains(out, "NoVNC URL") {
		t.Errorf("NoVNC URL line should be absent when liveURL is empty\nfull output:\n%s", out)
	}
}

func TestPrintDesktopStatus_amiIDConditional(t *testing.T) {
	d := &store.Desktop{DesktopID: "d-1", State: store.StateReady}

	var buf bytes.Buffer
	printDesktopStatus(&buf, d, "us-east-1", "")
	if strings.Contains(buf.String(), "AMI ID") {
		t.Error("AMI ID line should be absent when AMIID is empty")
	}

	d.AMIID = "ami-0abc"
	buf.Reset()
	printDesktopStatus(&buf, d, "us-east-1", "")
	if !strings.Contains(buf.String(), "AMI ID       : ami-0abc") {
		t.Errorf("AMI ID line missing\nfull output:\n%s", buf.String())
	}
}

func TestPrintDesktopStatus_reposAndSecrets(t *testing.T) {
	d := &store.Desktop{
		DesktopID: "d-1",
		State:     store.StateReady,
		Repos:     []string{"github.com/acme/app", "github.com/acme/lib"},
		Secrets:   []string{"prod/db-password", "prod/api-key"},
	}

	var buf bytes.Buffer
	printDesktopStatus(&buf, d, "us-east-1", "")
	out := buf.String()

	if !strings.Contains(out, "Repos        : github.com/acme/app, github.com/acme/lib") {
		t.Errorf("Repos line missing or incorrect\nfull output:\n%s", out)
	}
	if !strings.Contains(out, "Secrets      : prod/db-password, prod/api-key") {
		t.Errorf("Secrets line missing or incorrect\nfull output:\n%s", out)
	}
}

func TestPrintDesktopStatus_reposAndSecretsAbsentWhenEmpty(t *testing.T) {
	d := &store.Desktop{DesktopID: "d-1", State: store.StateReady}

	var buf bytes.Buffer
	printDesktopStatus(&buf, d, "us-east-1", "")
	out := buf.String()

	if strings.Contains(out, "Repos") {
		t.Error("Repos line should be absent when no repos")
	}
	if strings.Contains(out, "Secrets") {
		t.Error("Secrets line should be absent when no secrets")
	}
}

func TestPrintDesktopStatus_networkIntegrations(t *testing.T) {
	d := &store.Desktop{
		DesktopID:    "d-1",
		State:        store.StateReady,
		TailscaleNet: "acme-tailnet",
		StepCAServer: "ca.tailnet.ts.net",
	}

	var buf bytes.Buffer
	printDesktopStatus(&buf, d, "us-east-1", "")
	out := buf.String()

	if !strings.Contains(out, "Tailscale    : acme-tailnet") {
		t.Errorf("Tailscale line missing\nfull output:\n%s", out)
	}
	if !strings.Contains(out, "step-ca      : ca.tailnet.ts.net") {
		t.Errorf("step-ca line missing\nfull output:\n%s", out)
	}
}

func TestPrintDesktopStatus_failureFields(t *testing.T) {
	d := &store.Desktop{
		DesktopID:    "d-1",
		State:        store.StateFailed,
		FailurePhase: "provision",
		FailureMsg:   "timeout waiting for cloud-init",
	}

	var buf bytes.Buffer
	printDesktopStatus(&buf, d, "us-east-1", "")
	out := buf.String()

	if !strings.Contains(out, "Failure phase: provision") {
		t.Errorf("Failure phase missing\nfull output:\n%s", out)
	}
	if !strings.Contains(out, "Failure msg  : timeout waiting for cloud-init") {
		t.Errorf("Failure msg missing\nfull output:\n%s", out)
	}
}
