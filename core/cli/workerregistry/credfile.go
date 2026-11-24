package workerregistry

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mudler/LocalAI/pkg/atomicfile"
)

// CredentialFileName is the name of the file in the state directory of a worker
// that holds the own credential of its node.
const CredentialFileName = "node-credential.json"

// FileCredential keeps the own credential of a node in a file that only the
// owner can read. It is tied to the frontend and the node name: a worker that
// moves to another frontend, or runs under another name, does not present the
// credential of the old one.
type FileCredential struct {
	// Dir is the state directory of the worker.
	Dir string
	// FrontendURL and NodeName say whom the credential belongs to.
	FrontendURL string
	NodeName    string
}

type credentialRecord struct {
	Frontend  string `json:"frontend"`
	NodeName  string `json:"node_name"`
	NodeToken string `json:"node_token"`
}

func (f *FileCredential) path() string { return filepath.Join(f.Dir, CredentialFileName) }

func (f *FileCredential) frontend() string { return strings.TrimRight(f.FrontendURL, "/") }

// Load returns the stored credential, or empty when the file does not exist or
// belongs to another frontend or node name.
func (f *FileCredential) Load() (string, error) {
	data, err := os.ReadFile(f.path())
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", f.path(), err)
	}
	var rec credentialRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return "", fmt.Errorf("reading %s: %w", f.path(), err)
	}
	if rec.Frontend != f.frontend() || rec.NodeName != f.NodeName {
		return "", nil
	}
	return rec.NodeToken, nil
}

// Save writes the credential with mode 0600, in a directory that only the owner
// can enter.
func (f *FileCredential) Save(token string) error {
	if err := os.MkdirAll(f.Dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", f.Dir, err)
	}
	rec := credentialRecord{Frontend: f.frontend(), NodeName: f.NodeName, NodeToken: token}
	return atomicfile.WriteJSON(f.path(), rec, 0o600)
}
