package main

import (
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
