package downloader

import (
	"context"

	"github.com/mudler/xlog"
)

// FileTask describes one download operation and an optional post-download
// hook that runs after the bytes are present on disk. Callers keep any
// higher-level commit logic outside this helper.
type FileTask struct {
	URI           URI
	Destination   string
	SHA256        string
	FileIndex     int
	TotalFiles    int
	AfterDownload func(string) error
	Options       []DownloadOption
}

// DownloadFilesWithContext executes a set of file downloads sequentially.
// The helper centralizes the shared download path so callers only provide
// source/destination metadata and any post-download hook they need.
func DownloadFilesWithContext(ctx context.Context, tasks []FileTask, status func(string, string, string, float64), opts ...DownloadOption) error {
	for i := range tasks {
		task := tasks[i]
		if err := ctx.Err(); err != nil {
			return err
		}
		taskOpts := append([]DownloadOption{}, opts...)
		taskOpts = append(taskOpts, task.Options...)
		if err := downloadTaskWithRetry(ctx, task, status, taskOpts); err != nil {
			return err
		}
		if task.AfterDownload != nil {
			if err := task.AfterDownload(task.Destination); err != nil {
				return err
			}
		}
	}
	return nil
}

// downloadTaskWithRetry fetches one file, retrying transient failures. Without
// this, a single cancelled stream anywhere in a large multi-file repo threw
// away every file already downloaded, and the .partial resume machinery was
// unreachable because nothing ever made a second attempt.
func downloadTaskWithRetry(ctx context.Context, task FileTask, status func(string, string, string, float64), opts []DownloadOption) error {
	var err error
	for attempt := 1; ; attempt++ {
		err = task.URI.DownloadFileWithContext(ctx, task.Destination, task.SHA256, task.FileIndex, task.TotalFiles, status, opts...)
		if err == nil {
			return nil
		}
		if attempt >= DownloadRetryAttempts || !IsRetryable(ctx, err) {
			return err
		}
		xlog.Warn("download failed, retrying",
			"uri", string(task.URI),
			"destination", task.Destination,
			"attempt", attempt,
			"maxAttempts", DownloadRetryAttempts,
			"error", err,
		)
		if waitErr := waitBeforeRetry(ctx, attempt); waitErr != nil {
			return waitErr
		}
	}
}
