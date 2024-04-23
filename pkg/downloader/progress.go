package downloader

import "hash"

type progressWriter struct {
	fileName       string
	total          int64
	fileNo         int
	totalFiles     int
	written        int64
	downloadStatus func(string, string, string, float64)
	hash           hash.Hash
}

func (pw *progressWriter) Write(p []byte) (n int, err error) {
	n, err = pw.hash.Write(p)
	pw.written += int64(n)

	if pw.total > 0 {
		percentage := float64(pw.written) / float64(pw.total) * 100
		if pw.totalFiles > 1 {
			// This is a multi-file download
			// so we need to adjust the percentage
			// to reflect the progress of the whole download
			// This is the file pw.fileNo of pw.totalFiles files. We assume that
			// the files before successfully downloaded.
			percentage = percentage / float64(pw.totalFiles)
			if pw.fileNo > 1 {
				percentage += float64(pw.fileNo-1) * 100 / float64(pw.totalFiles)
			}
		}
		//log.Debug().Msgf("Downloading %s: %s/%s (%.2f%%)", pw.fileName, formatBytes(pw.written), formatBytes(pw.total), percentage)
		pw.downloadStatus(pw.fileName, formatBytes(pw.written), formatBytes(pw.total), percentage)
	} else {
		pw.downloadStatus(pw.fileName, formatBytes(pw.written), "", 0)
	}

	return
}
