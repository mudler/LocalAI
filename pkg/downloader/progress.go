package downloader

import (
	"context"
	"hash"
)

type progressWriter struct {
	fileName       string
	total          int64
	fileNo         int
	totalFiles     int
	written        int64
	downloadStatus func(string, string, string, float64)
	transferSink   TransferProgressSink
	hash           hash.Hash
	ctx            context.Context
}

func (pw *progressWriter) report() {
	if pw.transferSink != nil {
		pw.transferSink(TransferProgress{
			FileName: pw.fileName,
			Written:  pw.written,
			Total:    pw.total,
		})
	}
	if pw.downloadStatus == nil {
		return
	}
	percentage := float64(0)
	total := ""
	if pw.total > 0 {
		percentage = float64(pw.written) / float64(pw.total) * 100
		total = formatBytes(pw.total)
		if pw.totalFiles > 1 {
			percentage = percentage/float64(pw.totalFiles) + float64(pw.fileNo)*100/float64(pw.totalFiles)
		}
	}
	pw.downloadStatus(pw.fileName, formatBytes(pw.written), total, percentage)
}

func (pw *progressWriter) Write(p []byte) (n int, err error) {
	// Check for cancellation before writing
	if pw.ctx != nil {
		select {
		case <-pw.ctx.Done():
			return 0, pw.ctx.Err()
		default:
		}
	}

	n, err = pw.hash.Write(p)
	if err != nil {
		return n, err
	}
	pw.written += int64(n)

	// Check for cancellation after writing chunk
	if pw.ctx != nil {
		select {
		case <-pw.ctx.Done():
			return n, pw.ctx.Err()
		default:
		}
	}

	pw.report()

	return
}
