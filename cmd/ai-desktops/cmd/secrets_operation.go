package cmd

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"

	"github.com/orchael/ai-desktops/internal/store"
)

//go:embed scripts/secret-operation.py
var secretOperationPython string

// secretOperation owns one SSH process for the whole transaction. Closing stdin
// releases an idle lock; an interrupted rotation remains marked for recovery.
type secretOperation struct {
	cmd     *exec.Cmd
	in      io.WriteCloser
	encoder *json.Encoder
	decoder *json.Decoder
	pending bool
	closed  bool
}

func openSecretOperation(ctx context.Context, d *store.Desktop) (*secretOperation, error) {
	script := base64.StdEncoding.EncodeToString([]byte(secretOperationPython))
	remote := fmt.Sprintf("python3 -c 'import base64; exec(base64.b64decode(\"%s\"))'", script)
	args := append(sshFlags(d), "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=3", "ubuntu@"+d.Hostname, remote)
	return startSecretOperation(exec.CommandContext(ctx, "ssh", args...)) //nolint:gosec
}

func beginDesktopSecrets(ctx context.Context, s store.Store, d *store.Desktop, recoverPending bool) (*store.Desktop, *secretOperation, string, error) {
	return lockDesktopSecrets(ctx, s, d, recoverPending, openSecretOperation)
}

func lockDesktopSecrets(ctx context.Context, s store.Store, d *store.Desktop, recoverPending bool, open func(context.Context, *store.Desktop) (*secretOperation, error)) (*store.Desktop, *secretOperation, string, error) {
	ss, ok := s.(store.SecretStore)
	if !ok {
		return nil, nil, "", fmt.Errorf("fleet store does not support coordinated secrets")
	}
	op, err := open(ctx, d)
	if err != nil {
		return nil, nil, "", err
	}
	if op.pending && !recoverPending {
		op.Close()
		return nil, nil, "", fmt.Errorf("desktop has an interrupted secret operation; run secrets reload %s before add/remove", d.DesktopID)
	}
	// ALL_NEW both fences late commits from disconnected clients and returns the
	// authoritative path snapshot after the desktop lock was acquired.
	token := rand.Text()
	fresh, err := ss.BeginSecretOperation(ctx, d.DesktopID, token)
	if err != nil {
		op.Close()
		return nil, nil, "", fmt.Errorf("begin fleet secret operation: %w", err)
	}
	if fresh.Hostname != d.Hostname || fresh.InstanceID != d.InstanceID {
		op.Close()
		return nil, nil, "", fmt.Errorf("desktop target changed while acquiring secret coordination; retry")
	}
	return fresh, op, token, nil
}

func commitDesktopSecrets(ctx context.Context, s store.Store, id, token string, paths []string, op *secretOperation) error {
	ss, ok := s.(store.SecretStore)
	if !ok {
		return fmt.Errorf("fleet store does not support coordinated secrets")
	}
	if err := ss.CommitSecretOperation(ctx, id, token, paths); err != nil {
		return fmt.Errorf("commit fleet secret snapshot; run secrets reload %s to reconcile: %w", id, err)
	}
	if err := op.Commit(); err != nil {
		return err
	}
	fmt.Println("Secret snapshot committed. An active bridge was restarted and its old provider processes stopped. Start or resume sessions; start bridgectl first if it was already inactive.")
	return nil
}

func startSecretOperation(c *exec.Cmd) (*secretOperation, error) {
	in, err := c.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := c.StdoutPipe()
	if err != nil {
		_ = in.Close()
		return nil, err
	}
	op := &secretOperation{cmd: c, in: in, encoder: json.NewEncoder(in), decoder: json.NewDecoder(out)}
	if err := c.Start(); err != nil {
		_ = in.Close()
		_ = out.Close()
		return nil, fmt.Errorf("connect secret coordinator: %w", err)
	}
	if err := op.receive("ready"); err != nil {
		op.Close()
		return nil, err
	}
	return op, nil
}

func (op *secretOperation) receive(want string) error {
	var result struct {
		Status  string `json:"status"`
		Pending bool   `json:"pending"`
	}
	if err := op.decoder.Decode(&result); err != nil {
		return fmt.Errorf("secret coordinator disconnected; retry secrets reload to reconcile")
	}
	if result.Status == "busy" {
		return fmt.Errorf("desktop secrets are busy; another operation is active")
	}
	if result.Status != want {
		return fmt.Errorf("secret operation failed; run secrets reload to reconcile (check credential structure, private auth paths, and bridgectl service)")
	}
	if want == "ready" {
		op.pending = result.Pending
	}
	return nil
}

func (op *secretOperation) Run(script string) error {
	if err := op.encoder.Encode(map[string]string{"action": "run", "script": script}); err != nil {
		return fmt.Errorf("send secret rotation: %w", err)
	}
	return op.receive("rotated")
}

func (op *secretOperation) Commit() error {
	if err := op.encoder.Encode(map[string]string{"action": "commit"}); err != nil {
		return fmt.Errorf("acknowledge secret rotation: %w", err)
	}
	return op.receive("committed")
}

func (op *secretOperation) Close() {
	if !op.closed {
		op.closed = true
		_ = op.in.Close()
		_ = op.cmd.Wait()
	}
}
