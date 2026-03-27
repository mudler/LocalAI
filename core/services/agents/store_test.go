package agents

import (
	"context"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	t.Skip("requires docker")

	ctx := t.Context()

	pgC, err := tcpostgres.Run(ctx, "postgres:16",
		tcpostgres.WithDatabase("testdb"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategyAndDeadline(60*time.Second,
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2)),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { pgC.Terminate(context.Background()) })

	connStr, err := pgC.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	db, err := gorm.Open(postgres.Open(connStr), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open gorm: %v", err)
	}
	return db
}

func TestAgentStore_ObservablesByName(t *testing.T) {
	db := setupTestDB(t)

	store, err := NewAgentStore(db)
	if err != nil {
		t.Fatalf("NewAgentStore: %v", err)
	}

	// Insert observables for two different agents.
	obs1 := &AgentObservableRecord{
		AgentName:   "u1:agent",
		EventType:   "status",
		PayloadJSON: `{"msg":"hello from u1"}`,
	}
	obs2 := &AgentObservableRecord{
		AgentName:   "u2:agent",
		EventType:   "action",
		PayloadJSON: `{"msg":"hello from u2"}`,
	}
	if err := store.AppendObservable(obs1); err != nil {
		t.Fatalf("AppendObservable u1: %v", err)
	}
	if err := store.AppendObservable(obs2); err != nil {
		t.Fatalf("AppendObservable u2: %v", err)
	}

	results, err := store.GetObservables("u1:agent", 100)
	if err != nil {
		t.Fatalf("GetObservables: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 observable for u1:agent, got %d", len(results))
	}
	if results[0].AgentName != "u1:agent" {
		t.Errorf("AgentName = %q, want %q", results[0].AgentName, "u1:agent")
	}
	if results[0].PayloadJSON != `{"msg":"hello from u1"}` {
		t.Errorf("PayloadJSON = %q, want %q", results[0].PayloadJSON, `{"msg":"hello from u1"}`)
	}
}

func TestAgentStore_ClearObservables(t *testing.T) {
	db := setupTestDB(t)

	store, err := NewAgentStore(db)
	if err != nil {
		t.Fatalf("NewAgentStore: %v", err)
	}

	obs := &AgentObservableRecord{
		AgentName:   "clearme:agent",
		EventType:   "status",
		PayloadJSON: `{"msg":"will be cleared"}`,
	}
	if err := store.AppendObservable(obs); err != nil {
		t.Fatalf("AppendObservable: %v", err)
	}

	// Verify it exists.
	results, err := store.GetObservables("clearme:agent", 100)
	if err != nil {
		t.Fatalf("GetObservables before clear: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 observable before clear, got %d", len(results))
	}

	// Clear and verify empty.
	if err := store.ClearObservables("clearme:agent"); err != nil {
		t.Fatalf("ClearObservables: %v", err)
	}

	results, err = store.GetObservables("clearme:agent", 100)
	if err != nil {
		t.Fatalf("GetObservables after clear: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 observables after clear, got %d", len(results))
	}
}

func TestAgentStore_SaveAndGetConfig(t *testing.T) {
	db := setupTestDB(t)

	store, err := NewAgentStore(db)
	if err != nil {
		t.Fatalf("NewAgentStore: %v", err)
	}

	cfg := &AgentConfigRecord{
		UserID:     "user-1",
		Name:       "my-agent",
		ConfigJSON: `{"model":"gpt-4","connector":[]}`,
		Status:     "active",
	}
	if err := store.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	got, err := store.GetConfig("user-1", "my-agent")
	if err != nil {
		t.Fatalf("GetConfig: %v", err)
	}

	if got.UserID != "user-1" {
		t.Errorf("UserID = %q, want %q", got.UserID, "user-1")
	}
	if got.Name != "my-agent" {
		t.Errorf("Name = %q, want %q", got.Name, "my-agent")
	}
	if got.ConfigJSON != `{"model":"gpt-4","connector":[]}` {
		t.Errorf("ConfigJSON = %q, want %q", got.ConfigJSON, `{"model":"gpt-4","connector":[]}`)
	}
	if got.Status != "active" {
		t.Errorf("Status = %q, want %q", got.Status, "active")
	}
}

func TestAgentStore_ConfigUpsert(t *testing.T) {
	db := setupTestDB(t)

	store, err := NewAgentStore(db)
	if err != nil {
		t.Fatalf("NewAgentStore: %v", err)
	}

	// Save initial config.
	cfg := &AgentConfigRecord{
		UserID:     "user-2",
		Name:       "upsert-agent",
		ConfigJSON: `{"model":"gpt-3.5"}`,
		Status:     "active",
	}
	if err := store.SaveConfig(cfg); err != nil {
		t.Fatalf("SaveConfig (initial): %v", err)
	}

	originalID := cfg.ID

	// Save again with same UserID+Name but different config data.
	cfg2 := &AgentConfigRecord{
		UserID:     "user-2",
		Name:       "upsert-agent",
		ConfigJSON: `{"model":"gpt-4o"}`,
		Status:     "paused",
	}
	if err := store.SaveConfig(cfg2); err != nil {
		t.Fatalf("SaveConfig (upsert): %v", err)
	}

	// Verify it was updated, not duplicated.
	got, err := store.GetConfig("user-2", "upsert-agent")
	if err != nil {
		t.Fatalf("GetConfig after upsert: %v", err)
	}

	if got.ID != originalID {
		t.Errorf("ID changed after upsert: got %q, want %q", got.ID, originalID)
	}
	if got.ConfigJSON != `{"model":"gpt-4o"}` {
		t.Errorf("ConfigJSON not updated: got %q, want %q", got.ConfigJSON, `{"model":"gpt-4o"}`)
	}
	if got.Status != "paused" {
		t.Errorf("Status not updated: got %q, want %q", got.Status, "paused")
	}
}
