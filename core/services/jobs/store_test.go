package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
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

func TestNewJobStore_AutoMigrates(t *testing.T) {
	db := setupTestDB(t)

	store, err := NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore returned error: %v", err)
	}
	if store == nil {
		t.Fatal("NewJobStore returned nil store")
	}
}

func TestAppendJobTrace_SingleAppend(t *testing.T) {
	db := setupTestDB(t)

	store, err := NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}

	job := &JobRecord{
		TaskID:  "task-1",
		UserID:  "user-1",
		Status:  "running",
	}
	if err := store.CreateJob(job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if err := store.AppendJobTrace(job.ID, "status", "started processing"); err != nil {
		t.Fatalf("AppendJobTrace: %v", err)
	}

	got, err := store.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}

	var traces []map[string]string
	if err := json.Unmarshal([]byte(got.TracesJSON), &traces); err != nil {
		t.Fatalf("unmarshal traces: %v", err)
	}

	if len(traces) != 1 {
		t.Fatalf("expected 1 trace, got %d", len(traces))
	}
	if traces[0]["type"] != "status" || traces[0]["content"] != "started processing" {
		t.Errorf("unexpected trace: %+v", traces[0])
	}
}

func TestAppendJobTrace_AppendToExisting(t *testing.T) {
	db := setupTestDB(t)

	store, err := NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}

	// Create a job with one existing trace already in TracesJSON.
	existingTrace := []map[string]string{{"type": "init", "content": "initialized"}}
	existingJSON, _ := json.Marshal(existingTrace)

	job := &JobRecord{
		TaskID:     "task-2",
		UserID:     "user-1",
		Status:     "running",
		TracesJSON: string(existingJSON),
	}
	if err := store.CreateJob(job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if err := store.AppendJobTrace(job.ID, "action", "tool called"); err != nil {
		t.Fatalf("AppendJobTrace: %v", err)
	}

	got, err := store.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}

	var traces []map[string]string
	if err := json.Unmarshal([]byte(got.TracesJSON), &traces); err != nil {
		t.Fatalf("unmarshal traces: %v", err)
	}

	if len(traces) != 2 {
		t.Fatalf("expected 2 traces, got %d", len(traces))
	}
	if traces[0]["type"] != "init" {
		t.Errorf("first trace type = %q, want %q", traces[0]["type"], "init")
	}
	if traces[1]["type"] != "action" {
		t.Errorf("second trace type = %q, want %q", traces[1]["type"], "action")
	}
}

func TestAppendJobTrace_ConcurrentAppends(t *testing.T) {
	db := setupTestDB(t)

	store, err := NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}

	job := &JobRecord{
		TaskID: "task-concurrent",
		UserID: "user-1",
		Status: "running",
	}
	if err := store.CreateJob(job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	// Launch 10 goroutines, each appending a unique trace.
	const n = 10
	var wg sync.WaitGroup
	wg.Add(n)
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			errs[idx] = store.AppendJobTrace(job.ID, "step", fmt.Sprintf("step-%d", idx))
		}(i)
	}
	wg.Wait()

	for i, e := range errs {
		if e != nil {
			t.Errorf("goroutine %d: AppendJobTrace error: %v", i, e)
		}
	}

	got, err := store.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}

	var traces []map[string]string
	if err := json.Unmarshal([]byte(got.TracesJSON), &traces); err != nil {
		t.Fatalf("unmarshal traces: %v", err)
	}

	// BUG B1: The read-modify-write in AppendJobTrace is not atomic,
	// so concurrent appends can lose traces. This test documents the bug.
	if len(traces) != n {
		t.Errorf("expected %d traces after concurrent appends, got %d (race condition — bug B1)", n, len(traces))
	}
}

func TestCreateAndGetTask(t *testing.T) {
	db := setupTestDB(t)

	store, err := NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}

	task := &TaskRecord{
		UserID:      "user-42",
		Name:        "my-task",
		Description: "A test task",
		Model:       "gpt-4",
		Prompt:      "Do something",
		Enabled:     true,
	}
	if err := store.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if task.ID == "" {
		t.Fatal("expected task ID to be set after creation")
	}

	got, err := store.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}

	if got.UserID != "user-42" {
		t.Errorf("UserID = %q, want %q", got.UserID, "user-42")
	}
	if got.Name != "my-task" {
		t.Errorf("Name = %q, want %q", got.Name, "my-task")
	}
	if got.Description != "A test task" {
		t.Errorf("Description = %q, want %q", got.Description, "A test task")
	}
	if got.Model != "gpt-4" {
		t.Errorf("Model = %q, want %q", got.Model, "gpt-4")
	}
	if got.Prompt != "Do something" {
		t.Errorf("Prompt = %q, want %q", got.Prompt, "Do something")
	}
	if !got.Enabled {
		t.Error("Enabled = false, want true")
	}
}

func TestCreateAndGetJob(t *testing.T) {
	db := setupTestDB(t)

	store, err := NewJobStore(db)
	if err != nil {
		t.Fatalf("NewJobStore: %v", err)
	}

	job := &JobRecord{
		TaskID:      "task-99",
		UserID:      "user-99",
		Status:      "pending",
		TriggeredBy: "manual",
		FrontendID:  "frontend-1",
	}
	if err := store.CreateJob(job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}

	if job.ID == "" {
		t.Fatal("expected job ID to be set after creation")
	}

	got, err := store.GetJob(job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}

	if got.TaskID != "task-99" {
		t.Errorf("TaskID = %q, want %q", got.TaskID, "task-99")
	}
	if got.UserID != "user-99" {
		t.Errorf("UserID = %q, want %q", got.UserID, "user-99")
	}
	if got.Status != "pending" {
		t.Errorf("Status = %q, want %q", got.Status, "pending")
	}
	if got.TriggeredBy != "manual" {
		t.Errorf("TriggeredBy = %q, want %q", got.TriggeredBy, "manual")
	}
	if got.FrontendID != "frontend-1" {
		t.Errorf("FrontendID = %q, want %q", got.FrontendID, "frontend-1")
	}
}
