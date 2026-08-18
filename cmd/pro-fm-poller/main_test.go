package main

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"pro-fm-poller/pkg/poller"
)

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

func TestInitSenderPhoneRollsBackStateOnInitFailure(t *testing.T) {
	stateMgr := poller.NewStateManager()
	phone := "+40111222333"

	client, err := initSenderPhone(phone, filepath.Join(t.TempDir(), "wapp_40111222333.sqlite"), stateMgr, nil, "", nil, "", "", func(phone string, dbPath string, stateMgr *poller.StateManager, alerter poller.Alerter, baseURL string) (poller.WhatsAppClient, error) {
		return nil, fmt.Errorf("boom")
	})
	if err == nil {
		t.Fatal("expected initSenderPhone() to return an error")
	}
	if client != nil {
		t.Fatalf("client = %v, want nil", client)
	}

	state := stateMgr.Get()
	if len(state.Connections) != 0 {
		t.Fatalf("connections = %+v, want rollback to remove failed sender entry", state.Connections)
	}
}

func TestInitSenderPhoneKeepsStateOnSuccess(t *testing.T) {
	stateMgr := poller.NewStateManager()
	phone := "+40111222333"
	mockClient := &poller.MockWhatsAppClient{}

	client, err := initSenderPhone(phone, filepath.Join(t.TempDir(), "wapp_40111222333.sqlite"), stateMgr, nil, "", nil, "", "", func(phone string, dbPath string, stateMgr *poller.StateManager, alerter poller.Alerter, baseURL string) (poller.WhatsAppClient, error) {
		return mockClient, nil
	})
	if err != nil {
		t.Fatalf("initSenderPhone() error = %v", err)
	}
	if client != mockClient {
		t.Fatalf("client = %v, want %v", client, mockClient)
	}

	state := stateMgr.Get()
	if len(state.Connections) != 1 || state.Connections[0].Phone != phone || state.Connections[0].Status != poller.StatusInitializing {
		t.Fatalf("connections = %+v, want single initializing sender entry", state.Connections)
	}
}
