package tunnel

import (
	"fmt"
	"net"
	"os/exec"
)

// Mode selects the tunneling mechanism.
type Mode string

const (
	ModeSSM Mode = "ssm"
	ModeSSH Mode = "ssh"
)

// Config holds the parameters for establishing a tunnel to the desktop bridge.
type Config struct {
	DesktopID  string
	InstanceID string
	Hostname   string
	SSHKeyPath string
	SSHUser    string
	Region     string
	Profile    string // AWS CLI profile; passed as --profile when non-empty
	BridgePort int
	Mode       Mode
}

// Tunnel represents an established or requested port-forward tunnel.
type Tunnel struct {
	LocalPort  int
	RemotePort int
	Mode       Mode
}

// EphemeralPort finds a free local TCP port for use as the tunnel local endpoint.
func EphemeralPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("find ephemeral port: %w", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port, nil
}

// SSMCommand returns the exec.Cmd that starts an AWS SSM Session Manager
// port-forwarding session. The caller is responsible for starting and stopping
// the command.
func SSMCommand(cfg *Config, localPort int) *exec.Cmd {
	params := fmt.Sprintf(
		`{"portNumber":["%d"],"localPortNumber":["%d"]}`,
		cfg.BridgePort, localPort,
	)
	args := []string{
		"ssm", "start-session",
		"--target", cfg.InstanceID,
		"--document-name", "AWS-StartPortForwardingSession",
		"--parameters", params,
		"--region", cfg.Region,
	}
	if cfg.Profile != "" {
		args = append(args, "--profile", cfg.Profile)
	}
	return exec.Command("aws", args...)
}

// SSHCommand returns the exec.Cmd that starts an SSH local port-forwarding
// session. The caller is responsible for starting and stopping the command.
func SSHCommand(cfg *Config, localPort int) *exec.Cmd {
	user := cfg.SSHUser
	if user == "" {
		user = "ubuntu"
	}
	args := []string{
		"-N",
		"-L", fmt.Sprintf("127.0.0.1:%d:127.0.0.1:%d", localPort, cfg.BridgePort),
		"-i", cfg.SSHKeyPath,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		fmt.Sprintf("%s@%s", user, cfg.Hostname),
	}
	return exec.Command("ssh", args...)
}

// BridgeURL returns the URL for reaching the bridge through the local tunnel.
func BridgeURL(localPort int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", localPort)
}
