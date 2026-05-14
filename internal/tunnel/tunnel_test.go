package tunnel

import (
	"strings"
	"testing"
)

var testCfg = &Config{
	DesktopID:  "d-001",
	InstanceID: "i-0abc123",
	Hostname:   "d-001.desktops.orchael.dev",
	SSHKeyPath: "/home/user/.ssh/id_rsa",
	SSHUser:    "ubuntu",
	Region:     "us-east-1",
	BridgePort: 9445,
}

func TestSSMCommand(t *testing.T) {
	cmd := SSMCommand(testCfg, 19445)
	if cmd.Path == "" || !strings.HasSuffix(cmd.Args[0], "aws") {
		t.Errorf("expected aws command, got path=%q args[0]=%q", cmd.Path, cmd.Args[0])
	}
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "ssm") {
		t.Error("expected ssm in args")
	}
	if !strings.Contains(args, "i-0abc123") {
		t.Error("expected instance ID in args")
	}
	if !strings.Contains(args, "us-east-1") {
		t.Error("expected region in args")
	}
}

func TestSSHCommand(t *testing.T) {
	cmd := SSHCommand(testCfg, 19445)
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "ubuntu@d-001.desktops.orchael.dev") {
		t.Errorf("expected user@host in args: %s", args)
	}
	if !strings.Contains(args, "19445") {
		t.Error("expected local port in args")
	}
	if !strings.Contains(args, "-N") {
		t.Error("expected -N flag")
	}
}

func TestSSHCommand_defaultUser(t *testing.T) {
	cfg := *testCfg
	cfg.SSHUser = ""
	cmd := SSHCommand(&cfg, 19445)
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "ubuntu@") {
		t.Errorf("expected ubuntu@ default user: %s", args)
	}
}

func TestBridgeURL(t *testing.T) {
	url := BridgeURL(19445)
	if url != "http://127.0.0.1:19445" {
		t.Errorf("got %q", url)
	}
}

func TestEphemeralPort(t *testing.T) {
	port, err := EphemeralPort()
	if err != nil {
		t.Fatalf("EphemeralPort: %v", err)
	}
	if port <= 0 || port > 65535 {
		t.Errorf("port out of range: %d", port)
	}
}
