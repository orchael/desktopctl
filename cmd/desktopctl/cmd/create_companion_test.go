package cmd

import (
	"context"
	"errors"
	"testing"
)

type companionRepoProbe struct {
	publicErr error
	sshCalls  int
	tokenSeen string
}

func (p *companionRepoProbe) CheckAccessible(context.Context) error {
	p.sshCalls++
	return errors.New("host SSH credentials are unavailable")
}

func (p *companionRepoProbe) CheckPublicAccessible(context.Context) error {
	return p.publicErr
}

func (p *companionRepoProbe) CheckTokenAccessible(_ context.Context, token string) error {
	p.tokenSeen = token
	return nil
}

func TestCheckCompanionRepoUsesPublicHTTPSWithoutHostSSH(t *testing.T) {
	probe := &companionRepoProbe{}
	err := checkCompanionRepo(context.Background(), probe, "/github/secret", func(context.Context, string) (string, error) {
		t.Fatal("public repository should not load a token")
		return "", nil
	})
	if err != nil || probe.sshCalls != 0 || probe.tokenSeen != "" {
		t.Fatalf("unexpected check result: err=%v ssh=%d token=%q", err, probe.sshCalls, probe.tokenSeen)
	}
}

func TestCheckCompanionRepoUsesConfiguredSecretForPrivateRepo(t *testing.T) {
	probe := &companionRepoProbe{publicErr: errors.New("not public")}
	err := checkCompanionRepo(context.Background(), probe, "/github/secret", func(_ context.Context, path string) (string, error) {
		if path != "/github/secret" {
			t.Fatalf("unexpected secret path %q", path)
		}
		return "private-token", nil
	})
	if err != nil || probe.sshCalls != 0 || probe.tokenSeen != "private-token" {
		t.Fatalf("unexpected check result: err=%v ssh=%d token=%q", err, probe.sshCalls, probe.tokenSeen)
	}
}

func TestCheckCompanionRepoRejectsPrivateRepoWithoutSecret(t *testing.T) {
	probe := &companionRepoProbe{publicErr: errors.New("not public")}
	err := checkCompanionRepo(context.Background(), probe, "", nil)
	if err == nil || probe.sshCalls != 0 {
		t.Fatalf("expected public access error without SSH fallback: err=%v ssh=%d", err, probe.sshCalls)
	}
}
