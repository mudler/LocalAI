package downloader

import "context"

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
		if err := task.URI.DownloadFileWithContext(ctx, task.Destination, task.SHA256, task.FileIndex, task.TotalFiles, status, taskOpts...); err != nil {
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
