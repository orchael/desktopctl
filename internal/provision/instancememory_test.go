package provision

import "testing"

func TestInstanceMemoryGiB_knownTypes(t *testing.T) {
	cases := []struct {
		instanceType string
		wantGiB      int
	}{
		{"t3.nano", 1},
		{"t3.micro", 1},
		{"t3.small", 2},
		{"t3.medium", 4},
		{"t3.large", 8},
		{"t3.xlarge", 16},
		{"t3.2xlarge", 32},
		{"t3a.large", 8},
		{"t4g.large", 8},
		{"m5.large", 8},
		{"m5.4xlarge", 64},
		{"m6i.large", 8},
		{"m6i.2xlarge", 32},
		{"m7i.large", 8},
		{"c5.large", 4},
		{"c5.xlarge", 8},
		{"c6i.large", 4},
		{"r5.large", 16},
		{"r5.xlarge", 32},
		{"r6i.large", 16},
		{"r6i.2xlarge", 64},
	}
	for _, tc := range cases {
		t.Run(tc.instanceType, func(t *testing.T) {
			got := InstanceMemoryGiB(tc.instanceType)
			if got != tc.wantGiB {
				t.Errorf("InstanceMemoryGiB(%q) = %d, want %d", tc.instanceType, got, tc.wantGiB)
			}
		})
	}
}

func TestInstanceMemoryGiB_unknownType(t *testing.T) {
	if got := InstanceMemoryGiB("x99.mega"); got != 0 {
		t.Errorf("unknown instance type should return 0, got %d", got)
	}
}

func TestInstanceMemoryGiB_emptyString(t *testing.T) {
	if got := InstanceMemoryGiB(""); got != 0 {
		t.Errorf("empty instance type should return 0, got %d", got)
	}
}
