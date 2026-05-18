package store

import (
	"context"
	"errors"
	"testing"
)

func TestInMemoryAMIStore_SaveAndGet_HappyPath(t *testing.T) {
	s := NewInMemoryAMIStore()
	ctx := context.Background()

	rec := &AMIRecord{
		Region:        "us-east-1",
		AMIID:         "ami-abc123",
		CreatedAt:     "2024-01-01T00:00:00Z",
		NovncVersion:  "1.2.3",
		BridgeVersion: "0.1.0",
		GoVersion:     "1.22.0",
		UvVersion:     "0.2.0",
	}

	if err := s.SaveAMI(ctx, rec); err != nil {
		t.Fatalf("SaveAMI: %v", err)
	}

	got, err := s.GetAMI(ctx, "us-east-1", "ami-abc123")
	if err != nil {
		t.Fatalf("GetAMI: %v", err)
	}
	if got.Region != rec.Region {
		t.Errorf("Region: got %q, want %q", got.Region, rec.Region)
	}
	if got.AMIID != rec.AMIID {
		t.Errorf("AMIID: got %q, want %q", got.AMIID, rec.AMIID)
	}
	if got.CreatedAt != rec.CreatedAt {
		t.Errorf("CreatedAt: got %q, want %q", got.CreatedAt, rec.CreatedAt)
	}
	if got.NovncVersion != rec.NovncVersion {
		t.Errorf("NovncVersion: got %q, want %q", got.NovncVersion, rec.NovncVersion)
	}
	if got.BridgeVersion != rec.BridgeVersion {
		t.Errorf("BridgeVersion: got %q, want %q", got.BridgeVersion, rec.BridgeVersion)
	}
	if got.GoVersion != rec.GoVersion {
		t.Errorf("GoVersion: got %q, want %q", got.GoVersion, rec.GoVersion)
	}
	if got.UvVersion != rec.UvVersion {
		t.Errorf("UvVersion: got %q, want %q", got.UvVersion, rec.UvVersion)
	}
}

func TestInMemoryAMIStore_SaveAMI_EmptyRegionErrors(t *testing.T) {
	s := NewInMemoryAMIStore()
	ctx := context.Background()

	err := s.SaveAMI(ctx, &AMIRecord{Region: "", AMIID: "ami-123"})
	if err == nil {
		t.Error("expected error when Region is empty")
	}
}

func TestInMemoryAMIStore_SaveAMI_EmptyAMIIDErrors(t *testing.T) {
	s := NewInMemoryAMIStore()
	ctx := context.Background()

	err := s.SaveAMI(ctx, &AMIRecord{Region: "us-east-1", AMIID: ""})
	if err == nil {
		t.Error("expected error when AMIID is empty")
	}
}

func TestInMemoryAMIStore_SaveAMI_AutoSetsCreatedAt(t *testing.T) {
	s := NewInMemoryAMIStore()
	ctx := context.Background()

	rec := &AMIRecord{Region: "us-east-1", AMIID: "ami-auto"}
	if err := s.SaveAMI(ctx, rec); err != nil {
		t.Fatalf("SaveAMI: %v", err)
	}

	got, err := s.GetAMI(ctx, "us-east-1", "ami-auto")
	if err != nil {
		t.Fatalf("GetAMI: %v", err)
	}
	if got.CreatedAt == "" {
		t.Error("expected CreatedAt to be auto-set, got empty string")
	}
}

func TestInMemoryAMIStore_SaveAMI_PreserveCreatedAt(t *testing.T) {
	s := NewInMemoryAMIStore()
	ctx := context.Background()

	preset := "2020-06-15T12:00:00Z"
	rec := &AMIRecord{Region: "us-west-2", AMIID: "ami-preserve", CreatedAt: preset}
	if err := s.SaveAMI(ctx, rec); err != nil {
		t.Fatalf("SaveAMI: %v", err)
	}

	got, err := s.GetAMI(ctx, "us-west-2", "ami-preserve")
	if err != nil {
		t.Fatalf("GetAMI: %v", err)
	}
	if got.CreatedAt != preset {
		t.Errorf("CreatedAt: got %q, want %q", got.CreatedAt, preset)
	}
}

func TestInMemoryAMIStore_SaveAMI_OverwritesExistingRecord(t *testing.T) {
	s := NewInMemoryAMIStore()
	ctx := context.Background()

	original := &AMIRecord{Region: "eu-west-1", AMIID: "ami-overwrite", NovncVersion: "1.0.0"}
	if err := s.SaveAMI(ctx, original); err != nil {
		t.Fatalf("SaveAMI original: %v", err)
	}

	updated := &AMIRecord{Region: "eu-west-1", AMIID: "ami-overwrite", NovncVersion: "2.0.0"}
	if err := s.SaveAMI(ctx, updated); err != nil {
		t.Fatalf("SaveAMI updated: %v", err)
	}

	got, err := s.GetAMI(ctx, "eu-west-1", "ami-overwrite")
	if err != nil {
		t.Fatalf("GetAMI: %v", err)
	}
	if got.NovncVersion != "2.0.0" {
		t.Errorf("NovncVersion: got %q, want %q", got.NovncVersion, "2.0.0")
	}
}

func TestInMemoryAMIStore_ListAMIs_FiltersByRegion(t *testing.T) {
	s := NewInMemoryAMIStore()
	ctx := context.Background()

	// Two records in us-east-1, one in eu-west-1.
	_ = s.SaveAMI(ctx, &AMIRecord{Region: "us-east-1", AMIID: "ami-111"})
	_ = s.SaveAMI(ctx, &AMIRecord{Region: "us-east-1", AMIID: "ami-222"})
	_ = s.SaveAMI(ctx, &AMIRecord{Region: "eu-west-1", AMIID: "ami-333"})

	list, err := s.ListAMIs(ctx, "us-east-1")
	if err != nil {
		t.Fatalf("ListAMIs: %v", err)
	}
	if len(list) != 2 {
		t.Errorf("us-east-1: got %d records, want 2", len(list))
	}

	euList, err := s.ListAMIs(ctx, "eu-west-1")
	if err != nil {
		t.Fatalf("ListAMIs eu-west-1: %v", err)
	}
	if len(euList) != 1 {
		t.Errorf("eu-west-1: got %d records, want 1", len(euList))
	}
}

func TestInMemoryAMIStore_ListAMIs_EmptyStore(t *testing.T) {
	s := NewInMemoryAMIStore()
	ctx := context.Background()

	list, err := s.ListAMIs(ctx, "us-east-1")
	if err != nil {
		t.Fatalf("ListAMIs: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected empty slice, got %d items", len(list))
	}
}

func TestInMemoryAMIStore_GetAMI_NotFound(t *testing.T) {
	s := NewInMemoryAMIStore()
	ctx := context.Background()

	_, err := s.GetAMI(ctx, "us-east-1", "ami-missing")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestInMemoryAMIStore_DeleteAMI_RemovesRecord(t *testing.T) {
	s := NewInMemoryAMIStore()
	ctx := context.Background()

	_ = s.SaveAMI(ctx, &AMIRecord{Region: "us-east-1", AMIID: "ami-delete"})

	if err := s.DeleteAMI(ctx, "us-east-1", "ami-delete"); err != nil {
		t.Fatalf("DeleteAMI: %v", err)
	}

	_, err := s.GetAMI(ctx, "us-east-1", "ami-delete")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestInMemoryAMIStore_DeleteAMI_NonExistentIsNoop(t *testing.T) {
	s := NewInMemoryAMIStore()
	ctx := context.Background()

	if err := s.DeleteAMI(ctx, "us-east-1", "ami-nonexistent"); err != nil {
		t.Errorf("expected no error deleting non-existent key, got %v", err)
	}
}
