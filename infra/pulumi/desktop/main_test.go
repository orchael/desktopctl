package main

import (
	"reflect"
	"testing"
)

func TestValidateDesktopAllowedPorts(t *testing.T) {
	got, err := validateDesktopAllowedPorts([]int{3000, 443, 3000, 9000})
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{3000, 443, 9000}; !reflect.DeepEqual(got, want) {
		t.Fatalf("validateDesktopAllowedPorts = %v, want %v", got, want)
	}
	for _, port := range []int{-1, 0, 22, 65536} {
		if _, err := validateDesktopAllowedPorts([]int{port}); err == nil {
			t.Errorf("port %d should be rejected", port)
		}
	}
}
