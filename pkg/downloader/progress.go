package downloader

import "hash"

type progressWriter struct {
	fileName       string
	total          int64
	written        int64
	downloadStatus func(string, string, string, float64)
	hash           hash.Hash
}

func (pw *progressWriter) Write(p []byte) (n int, err error) {
	n, err = pw.hash.Write(p)
	pw.written += int64(n)

	if pw.total > 0 {
		percentage := float64(pw.written) / float64(pw.total) * 100
		//log.Debug().Msgf("Downloading %s: %s/%s (%.2f%%)", pw.fileName, formatBytes(pw.written), formatBytes(pw.total), percentage)
		pw.downloadStatus(pw.fileName, formatBytes(pw.written), formatBytes(pw.total), percentage)
	} else {
		pw.downloadStatus(pw.fileName, formatBytes(pw.written), "", 0)
	}

	return
}
