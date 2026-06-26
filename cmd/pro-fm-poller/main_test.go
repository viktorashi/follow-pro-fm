package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"pro-fm-poller/pkg/poller"
)

func TestBootstrapSenderPhonesAlwaysIncludesCanonicalAndPersistedSecondaries(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "wapp.sqlite")

	for _, name := range []string{
		"wapp_40770661491.sqlite",
		"wapp_40734788254.sqlite",
		"wapp_40711122334.sqlite",
	} {
		if err := os.WriteFile(filepath.Join(tempDir, name), []byte("paired"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", name, err)
		}
	}

	got := bootstrapSenderPhones(dbPath)
	want := []string{
		poller.CanonicalSenderPhone,
		"+40711122334",
		"+40770661491",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bootstrapSenderPhones() = %v, want %v", got, want)
	}
}

func TestBootstrapSenderPhonesFallsBackToCanonicalOnly(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "wapp.sqlite")

	got := bootstrapSenderPhones(dbPath)
	want := []string{poller.CanonicalSenderPhone}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("bootstrapSenderPhones() = %v, want %v", got, want)
	}
}

func TestGatheringSettingLoadsFromDatabaseIntoStateManager(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "app.sqlite")
	dbMgr, err := poller.NewDBManager(dbPath)
	if err != nil {
		t.Fatalf("NewDBManager() error = %v", err)
	}
	if err := dbMgr.SetGatheringSignatures(context.Background(), false); err != nil {
		t.Fatalf("SetGatheringSignatures(false) error = %v", err)
	}

	stateMgr := poller.NewStateManager()
	gatheringEnabled, err := dbMgr.IsGatheringSignaturesEnabled(context.Background())
	if err != nil {
		t.Fatalf("IsGatheringSignaturesEnabled() error = %v", err)
	}
	stateMgr.Update(func(s *poller.AppState) {
		s.GatheringSignatures = gatheringEnabled
	})

	if stateMgr.Get().GatheringSignatures {
		t.Fatal("expected state manager to reflect persisted disabled gathering setting")
	}
}
