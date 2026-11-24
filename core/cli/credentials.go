package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mudler/xlog"

	"github.com/mudler/LocalAI/pkg/credentials"
)

// credentialsFileName is looked up in the data path when no flag is given, so
// an install that keeps all its state on one volume needs no extra setting.
const credentialsFileName = "credentials.yaml"

func resolveCredentialsFile(flag, dataPath string) string {
	if flag != "" {
		return flag
	}
	if dataPath == "" {
		return ""
	}
	p := filepath.Join(dataPath, credentialsFileName)
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// LoadCredentials installs the download credentials for this process. An empty
// path leaves downloads anonymous; registries still honor docker config.
func LoadCredentials(path string) error {
	if path == "" {
		return nil
	}
	store, err := credentials.Load(path, os.LookupEnv)
	if err != nil {
		return fmt.Errorf("loading credentials file %q: %w", path, err)
	}
	credentials.SetDefault(store)
	xlog.Info("Loaded download credentials", "file", path, "rules", store.Len())
	return nil
}
