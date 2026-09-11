package downloader

import (
	"context"
	"sync"

	"github.com/mudler/xlog"
	"golang.org/x/sync/errgroup"
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
	return DownloadFilesWithConcurrency(ctx, tasks, status, 1, opts...)
}

// DownloadFilesWithConcurrency runs up to concurrency downloads at once. A
// concurrency of one or less keeps the original sequential path, so callers that
// have not opted in are byte-for-byte unaffected: tasks still run in slice order
// and the first failure still returns before any later task starts.
//
// Only whole files run in parallel. A single file is never split, so the
// .partial resume machinery and the per-file SHA check in downloadTaskWithRetry
// keep working untouched.
//
// The status callback is serialized, because it belongs to the caller and the
// sequential path gave it an implicit guarantee of never being entered twice at
// once. AfterDownload is deliberately *not* serialized: it does the per-file
// verify-and-promote work that parallelism is meant to overlap, so hooks must be
// safe to run concurrently with each other.
func DownloadFilesWithConcurrency(ctx context.Context, tasks []FileTask, status func(string, string, string, float64), concurrency int, opts ...DownloadOption) error {
	if concurrency < 1 {
		concurrency = 1
	}

	var aggregateTotal int64
	aggregateAvailable := status != nil && len(tasks) > 0
	if aggregateAvailable {
		for _, task := range tasks {
			size, err := task.URI.ContentLength(ctx)
			if err != nil || size < 0 {
				aggregateAvailable = false
				break
			}
			aggregateTotal += size
		}
	}
	if aggregateTotal <= 0 {
		aggregateAvailable = false
	}

	if status != nil && concurrency > 1 && !aggregateAvailable {
		var statusMutex sync.Mutex
		unsynchronized := status
		status = func(fileName, current, total string, percent float64) {
			statusMutex.Lock()
			defer statusMutex.Unlock()
			unsynchronized(fileName, current, total, percent)
		}
	}

	// errgroup.WithContext cancels the derived context on the first error, which
	// is what stops in-flight transfers instead of letting them run to
	// completion, and Wait reports that first error rather than the
	// context.Canceled the siblings observe.
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(concurrency)
	aggregateWritten := make([]int64, len(tasks))
	var aggregateMutex sync.Mutex

	for i := range tasks {
		task := tasks[i]
		taskIndex := i
		if err := groupCtx.Err(); err != nil {
			break
		}
		group.Go(func() error {
			if err := groupCtx.Err(); err != nil {
				return err
			}
			taskOpts := append([]DownloadOption{}, opts...)
			taskOpts = append(taskOpts, task.Options...)
			taskStatus := status
			if aggregateAvailable {
				existingSink := applyDownloadOptions(taskOpts).transferProgress
				taskOpts = append(taskOpts, WithTransferProgress(func(event TransferProgress) {
					if existingSink != nil {
						existingSink(event)
					}
					aggregateMutex.Lock()
					aggregateWritten[taskIndex] = event.Written
					var written int64
					for _, taskWritten := range aggregateWritten {
						written += taskWritten
					}
					if status != nil {
						status(event.FileName, formatBytes(written), formatBytes(aggregateTotal), float64(written)*100/float64(aggregateTotal))
					}
					aggregateMutex.Unlock()
				}))
				taskStatus = nil
			}
			if err := downloadTaskWithRetry(groupCtx, task, taskStatus, taskOpts); err != nil {
				return err
			}
			if task.AfterDownload != nil {
				return task.AfterDownload(task.Destination)
			}
			return nil
		})
	}

	if err := group.Wait(); err != nil {
		return err
	}
	// A caller-cancelled context with no task in flight leaves the group clean,
	// so report the cancellation the sequential loop would have reported.
	return ctx.Err()
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
