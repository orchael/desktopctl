package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAssumeOnlyPolicyScopesToRole(t *testing.T) {
	t.Parallel()
	raw, err := policyJSON(assumeOnlyPolicy("arn:aws:iam::123456789012:role/test"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, "sts:AssumeRole") {
		t.Fatalf("policy does not include AssumeRole: %s", raw)
	}
	if strings.Contains(raw, "\"Resource\":\"*\"") {
		t.Fatalf("user policy should not allow broad role assumption: %s", raw)
	}
}

func TestControlPlaneRolePolicyIncludesRequiredResources(t *testing.T) {
	t.Parallel()
	raw, err := controlPlaneRolePolicy(
		"ai-desktops-fleet-dev",
		"ai-desktops-ami-dev",
		"state-bucket",
		"arn:aws:route53:::hostedzone/Z123",
		"arn:aws:iam::123456789012:role/desktop",
		"/ai-desktops/",
	)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("policy is not JSON: %v", err)
	}
	for _, want := range []string{"ai-desktops-fleet-dev", "state-bucket", "arn:aws:route53:::hostedzone/Z123"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("policy missing %q: %s", want, raw)
		}
	}
	for _, forbidden := range []string{"StringLikeIfExists", "\"Sid\":\"Secrets\",\"Effect\":\"Allow\""} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("policy contains broad secret condition %q: %s", forbidden, raw)
		}
	}
	for _, want := range []string{
		"arn:aws:secretsmanager:*:*:secret:/ai-desktops/*",
		"arn:aws:ssm:*:*:parameter/ai-desktops/*",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("policy missing scoped namespace ARN %q: %s", want, raw)
		}
	}
}
