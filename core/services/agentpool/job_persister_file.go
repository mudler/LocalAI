package agentpool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mudler/LocalAI/core/schema"
	"github.com/mudler/LocalAI/pkg/xsync"
	"github.com/mudler/xlog"
)

// fileJobPersister persists tasks and jobs to JSON files.
//
// Jobs serialize the service's in-memory jobs syncmap on each save (bulk write).
// Tasks are kept in this persister's own taskSet map instead: the tasks SyncedMap
// calls SaveTask/DeleteTask while holding its internal lock (write-through), so
// reading back the SyncedMap here would re-enter that lock and deadlock. The
// self-contained taskSet, seeded by LoadTasks, lets a per-task write rewrite the
// whole bulk file without touching the SyncedMap.
//
// Runtime reads (GetJob/ListJobs) return nil (the in-memory state is the
// authoritative source); LoadTasks/LoadJobs bootstrap state at startup.
type fileJobPersister struct {
	jobs      *xsync.SyncedMap[string, schema.Job]
	tasksFile string
	jobsFile  string
	mu        sync.Mutex
	// taskSet is the persister's own view of all tasks, seeded by LoadTasks and
	// updated by SaveTask/DeleteTask. The bulk JSON file is rewritten from it.
	taskSet map[string]schema.Task
}

func (p *fileJobPersister) SaveTask(_ string, task schema.Task) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.taskSet[task.ID] = task
	return p.writeTasksLocked()
}

func (p *fileJobPersister) DeleteTask(taskID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.taskSet, taskID)
	return p.writeTasksLocked()
}

func (p *fileJobPersister) SaveJob(_ string, _ schema.Job) error {
	return p.saveJobsToFile()
}

func (p *fileJobPersister) DeleteJob(_ string) error {
	return p.saveJobsToFile()
}

func (p *fileJobPersister) FlushTasks() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.writeTasksLocked()
}

func (p *fileJobPersister) FlushJobs() error {
	return p.saveJobsToFile()
}

// GetJob returns nil — file persister has no authoritative reads.
func (p *fileJobPersister) GetJob(_ string) (*schema.Job, error) {
	return nil, nil
}

// ListJobs returns nil — file persister has no authoritative reads.
func (p *fileJobPersister) ListJobs(_, _, _ string, _ int) ([]schema.Job, error) {
	return nil, nil
}

func (p *fileJobPersister) LoadTasks(_ string) ([]schema.Task, error) {
	if p.tasksFile == "" {
		return nil, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if _, err := os.Stat(p.tasksFile); os.IsNotExist(err) {
		xlog.Debug("agent_tasks.json not found, starting with empty tasks")
		return nil, nil
	}

	data, err := os.ReadFile(p.tasksFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read tasks file: %w", err)
	}

	var tf schema.TasksFile
	if err := json.Unmarshal(data, &tf); err != nil {
		return nil, fmt.Errorf("failed to parse tasks file: %w", err)
	}

	// Seed the in-memory set so subsequent per-task SaveTask/DeleteTask merge into
	// (rather than overwrite) the persisted tasks when the bulk file is rewritten.
	for _, t := range tf.Tasks {
		p.taskSet[t.ID] = t
	}

	xlog.Info("Loaded tasks from file", "count", len(tf.Tasks))
	return tf.Tasks, nil
}

func (p *fileJobPersister) LoadJobs(_ string) ([]schema.Job, error) {
	if p.jobsFile == "" {
		return nil, nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if _, err := os.Stat(p.jobsFile); os.IsNotExist(err) {
		xlog.Debug("agent_jobs.json not found, starting with empty jobs")
		return nil, nil
	}

	data, err := os.ReadFile(p.jobsFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read jobs file: %w", err)
	}

	var jf schema.JobsFile
	if err := json.Unmarshal(data, &jf); err != nil {
		return nil, fmt.Errorf("failed to parse jobs file: %w", err)
	}

	xlog.Info("Loaded jobs from file", "count", len(jf.Jobs))
	return jf.Jobs, nil
}

func (p *fileJobPersister) CleanupOldJobs(_ time.Duration) (int64, error) {
	return 0, nil // cleanup handled via in-memory filtering
}

// writeTasksLocked serializes the persister's task set to the JSON file. Callers
// must hold p.mu.
func (p *fileJobPersister) writeTasksLocked() error {
	if p.tasksFile == "" {
		return nil
	}

	tasks := make([]schema.Task, 0, len(p.taskSet))
	for _, t := range p.taskSet {
		tasks = append(tasks, t)
	}

	tf := schema.TasksFile{Tasks: tasks}

	data, err := json.MarshalIndent(tf, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal tasks: %w", err)
	}

	return writeFileAtomic(p.tasksFile, data, 0600)
}

// saveJobsToFile serializes the entire jobs map to the JSON file.
func (p *fileJobPersister) saveJobsToFile() error {
	if p.jobsFile == "" {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	jf := schema.JobsFile{
		Jobs:        p.jobs.Values(),
		LastCleanup: time.Now(),
	}

	data, err := json.MarshalIndent(jf, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal jobs: %w", err)
	}

	return writeFileAtomic(p.jobsFile, data, 0600)
}

// writeFileAtomic writes data to path via a same-directory temp file + rename.
// os.WriteFile opens with O_TRUNC, so a concurrent reader can land between the
// truncate and the write and see an empty file ("unexpected end of JSON input").
// rename(2) is atomic on POSIX, so readers see either the prior contents or the
// new contents and never a zero-byte window.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	removeTmp := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		removeTmp()
		return fmt.Errorf("failed to write temp file: %w", err)
	}
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		removeTmp()
		return fmt.Errorf("failed to chmod temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		removeTmp()
		return fmt.Errorf("failed to sync temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		removeTmp()
		return fmt.Errorf("failed to close temp file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		removeTmp()
		return fmt.Errorf("failed to rename temp file: %w", err)
	}
	return nil
}
