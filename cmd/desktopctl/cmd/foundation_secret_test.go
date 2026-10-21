package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	smtypes "github.com/aws/aws-sdk-go-v2/service/secretsmanager/types"
)

type fakeSecretDescriber struct {
	occupied map[string]bool
	otherErr error
}

func (f fakeSecretDescriber) DescribeSecret(_ context.Context, input *secretsmanager.DescribeSecretInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.DescribeSecretOutput, error) {
	if f.otherErr != nil {
		return nil, f.otherErr
	}
	if f.occupied[aws.ToString(input.SecretId)] {
		return &secretsmanager.DescribeSecretOutput{}, nil
	}
	return nil, &smtypes.ResourceNotFoundException{}
}

func TestSelectFreshOperatorSecretName(t *testing.T) {
	base := "/desktopctl/prod-ca-central-1/control-plane/aws-operator"
	for _, tc := range []struct {
		name     string
		occupied map[string]bool
		want     string
	}{
		{"first installation", nil, base},
		{"old name pending deletion", map[string]bool{base: true}, base + "-v2"},
		{"repeated recreation", map[string]bool{base: true, base + "-v2": true}, base + "-v3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectFreshOperatorSecretName(context.Background(), fakeSecretDescriber{occupied: tc.occupied}, "prod", "ca-central-1")
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
	_, err := selectFreshOperatorSecretName(context.Background(), fakeSecretDescriber{otherErr: errors.New("access denied")}, "prod", "ca-central-1")
	if err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("expected DescribeSecret failure, got %v", err)
	}
}
