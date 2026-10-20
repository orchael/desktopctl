package main

import (
	"encoding/json"
	"slices"
	"testing"
)

func TestControlPlanePolicyAllowsAMICopy(t *testing.T) {
	var policy struct {
		Statement []struct {
			Sid    string   `json:"Sid"`
			Action []string `json:"Action"`
		} `json:"Statement"`
	}
	if err := json.Unmarshal([]byte(controlPlanePolicy("aws", "123456789012", "test-state")), &policy); err != nil {
		t.Fatal(err)
	}
	for _, statement := range policy.Statement {
		if statement.Sid == "EC2AndPackerControlPlane" {
			if !slices.Contains(statement.Action, "ec2:CopyImage") {
				t.Fatal("EC2AndPackerControlPlane must allow ec2:CopyImage for multi-region AMI builds")
			}
			return
		}
	}
	t.Fatal("EC2AndPackerControlPlane statement not found")
}
