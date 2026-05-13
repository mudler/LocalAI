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
// It holds references to the service's syncmaps and serializes the entire
// map contents on each save (bulk write). Reads at runtime return nil
// (the in-memory map is the authoritative source); LoadTasks/LoadJobs
// are used only at startup to bootstrap the syncmaps.
type fileJobPersister struct {
	tasks     *xsync.SyncedMap[string, schema.Task]
	jobs      *xsync.SyncedMap[string, schema.Job]
	tasksFile string
	jobsFile  string
	mu        sync.Mutex
}

func (p *fileJobPersister) SaveTask(_ string, _ schema.Task) error {
	return p.saveTasksToFile()
}

func (p *fileJobPersister) DeleteTask(_ string) error {
	return p.saveTasksToFile()
}

func (p *fileJobPersister) SaveJob(_ string, _ schema.Job) error {
	return p.saveJobsToFile()
}

func (p *fileJobPersister) DeleteJob(_ string) error {
	return p.saveJobsToFile()
}

func (p *fileJobPersister) FlushTasks() error {
	return p.saveTasksToFile()
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

// saveTasksToFile serializes the entire tasks map to the JSON file.
func (p *fileJobPersister) saveTasksToFile() error {
	if p.tasksFile == "" {
		return nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	tf := schema.TasksFile{
		Tasks: p.tasks.Values(),
	}

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
