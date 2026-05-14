package backend

import "testing"

func TestBackendURL(t *testing.T) {
	c := &Config{BackendBucket: "my-bucket"}
	if got := c.BackendURL(); got != "s3://my-bucket" {
		t.Errorf("BackendURL: got %q", got)
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{"valid", Config{Region: "us-east-1", BackendBucket: "b", FleetTable: "t"}, false},
		{"missing region", Config{BackendBucket: "b", FleetTable: "t"}, true},
		{"missing bucket", Config{Region: "us-east-1", FleetTable: "t"}, true},
		{"missing table", Config{Region: "us-east-1", BackendBucket: "b"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate: got err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestFleetTableSchema(t *testing.T) {
	s := FleetTableSchema("my-fleet")
	if s.TableName != "my-fleet" {
		t.Errorf("TableName: got %q", s.TableName)
	}
	if s.PKName != "desktop_id" {
		t.Errorf("PKName: got %q", s.PKName)
	}
	if s.PKType != "S" {
		t.Errorf("PKType: got %q", s.PKType)
	}
}

func TestDefaultBucketConfig(t *testing.T) {
	bc := DefaultBucketConfig("my-bucket", "us-east-1")
	if !bc.Versioning {
		t.Error("Versioning should be true")
	}
	if !bc.SSEEnabled {
		t.Error("SSEEnabled should be true")
	}
	if !bc.BlockPublicAccess {
		t.Error("BlockPublicAccess should be true")
	}
}
