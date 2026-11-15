package remotes

import (
	"context"
	"errors"
	"time"
)

// Names that the external tests need from the package.
type (
	Record     = record
	OwnedEntry = ownedEntry
)

const (
	JSONFileName = jsonFileName
	JSONKeyName  = jsonKeyName
	SealKeySize  = sealKeySize
)

var NewSealKey = newSealKey

// ListUpstream runs the lister of a kind with the given limits and returns
// the models, or the step and message of the failure.
func ListUpstream(ctx context.Context, kind Kind, rawURL, key string, timeout time.Duration, maxBody int64) (models []UpstreamModel, step, message string, err error) {
	t, err := parseTarget(kind, rawURL, key)
	if err != nil {
		var ue *upstreamError
		if errors.As(err, &ue) {
			return nil, ue.Step, ue.Message, err
		}
		return nil, "", err.Error(), err
	}
	f := fetcher{timeout: timeout, maxBody: maxBody}
	models, _, err = f.list(ctx, t)
	var ue *upstreamError
	if errors.As(err, &ue) {
		return nil, ue.Step, ue.Message, err
	}
	return models, "", "", err
}

var (
	LooksLikeChatModel = looksLikeChatModel
	ValidUsecases      = validUsecases
)

// EntryUpstreamURL returns the proxy.upstream_url of an entry.
func EntryUpstreamURL(kind Kind, rawURL string) (string, error) {
	t, err := parseTarget(kind, rawURL, "")
	if err != nil {
		return "", err
	}
	return t.entryUpstreamURL(), nil
}

type ModelDiff = modelDiff

var (
	SanitizeName    = sanitizeName
	RemoteSlug      = remoteSlug
	ChooseEntryName = chooseEntryName
	ModelDiffOf     = diffModels
	HashBytes       = hashBytes
)

// BuildEntry returns the YAML of an entry for a remote.
func BuildEntry(kind Kind, rawURL, name, remoteID string, m UpstreamModel) ([]byte, error) {
	t, err := parseTarget(kind, rawURL, "")
	if err != nil {
		return nil, err
	}
	return buildEntry(t, name, remoteID, m)
}

// TargetBase returns the address a key of the remote is bound to.
func TargetBase(kind Kind, rawURL string) (string, error) {
	t, err := parseTarget(kind, rawURL, "")
	if err != nil {
		return "", err
	}
	return t.base.String(), nil
}

// SetNow replaces the clock of a service made by New.
func SetNow(svc Service, now func() time.Time) { svc.(*service).now = now }
