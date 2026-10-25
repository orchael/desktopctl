package main

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

func TestAdditionalIngressPorts(t *testing.T) {
	got, err := additionalIngressPorts([]int{443, 3000, 8443, 3000, 9000})
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{3000, 9000}; !reflect.DeepEqual(got, want) {
		t.Fatalf("additionalIngressPorts = %v, want %v", got, want)
	}
	for _, port := range []int{0, -1, 22, 65536} {
		if _, err := additionalIngressPorts([]int{port}); err == nil {
			t.Errorf("port %d should be rejected", port)
		}
	}
}

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
