package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestRegionDefaultsAndPersistsAcrossReopen(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "rice.db")

	database, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	region, err := database.Region(ctx, "user-1")
	if err != nil {
		t.Fatalf("Region() error = %v", err)
	}
	if region != "us" {
		t.Errorf("default Region() = %q, want us", region)
	}
	if err := database.SetRegion(ctx, "user-1", "gb"); err != nil {
		t.Fatalf("SetRegion() error = %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	defer reopened.Close()
	region, err = reopened.Region(ctx, "user-1")
	if err != nil {
		t.Fatalf("Region() after reopen error = %v", err)
	}
	if region != "gb" {
		t.Errorf("persisted Region() = %q, want gb", region)
	}
	otherRegion, err := reopened.Region(ctx, "user-2")
	if err != nil {
		t.Fatal(err)
	}
	if otherRegion != "us" {
		t.Errorf("other user's Region() = %q, want us", otherRegion)
	}
}
