package modelsearch

import (
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mudler/LocalAI/pkg/httpclient"
)

// userAgent identifies LocalAI to the providers.
const userAgent = "LocalAI model search (+https://github.com/mudler/LocalAI)"

// maxBody caps what is read from a provider answer.
const maxBody = 4 << 20

func newHTTPClient() *http.Client {
	return httpclient.NewWithTimeout(20*time.Second, httpclient.WithFollowRedirects())
}

// readBody reads at most maxBody bytes and errors when the answer is larger.
func readBody(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxBody {
		return nil, fmt.Errorf("answer larger than %d bytes", maxBody)
	}
	return b, nil
}
