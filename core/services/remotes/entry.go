package remotes

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/pkg/model"
)

// maxNameLen keeps an entry name, and so its file name, well below the limit
// of common file systems.
const maxNameLen = 120

// sanitizeName turns an upstream id into a model name that is safe as a file
// name: letters, digits, dot, dash and underscore stay, everything else
// (slash, colon, space) becomes a dash. It is deterministic. It returns ""
// when nothing usable is left.
func sanitizeName(id string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(id) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	s := b.String()
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	for strings.Contains(s, "..") {
		s = strings.ReplaceAll(s, "..", ".")
	}
	s = strings.Trim(s, "-.")
	if len(s) > maxNameLen {
		s = strings.Trim(s[:maxNameLen], "-.")
	}
	return s
}

// remoteSlug is the prefix an entry name gets when the plain name is
// taken.
func remoteSlug(name string) string {
	s := strings.ToLower(sanitizeName(name))
	if s == "" {
		return "remote"
	}
	return s
}

// chooseEntryName picks the name of the entry for an upstream id: the
// sanitised id when it is free, else "<remote-slug>-<id>". ok is false
// when the id has no usable characters or both names are taken. A name that is
// free was neither a model config nor claimed by any remote.
func chooseEntryName(id, slug string, taken func(string) bool) (name string, ok bool) {
	plain := sanitizeName(id)
	if plain == "" {
		return "", false
	}
	if !taken(plain) {
		return plain, true
	}
	prefixed := sanitizeName(slug + "-" + plain)
	if prefixed != "" && prefixed != plain && !taken(prefixed) {
		return prefixed, true
	}
	return "", false
}

type entryProxy struct {
	UpstreamURL   string `yaml:"upstream_url"`
	UpstreamModel string `yaml:"upstream_model"`
	Mode          string `yaml:"mode,omitempty"`
	Provider      string `yaml:"provider,omitempty"`
	Remote        string `yaml:"remote"`
}

type entryFile struct {
	Name          string     `yaml:"name"`
	Backend       string     `yaml:"backend"`
	KnownUsecases []string   `yaml:"known_usecases,omitempty"`
	Proxy         entryProxy `yaml:"proxy"`
}

const entryHeader = "# Created from a remote. You can edit it: a refresh never rewrites this file.\n"

// buildEntry returns the YAML of an entry. The file holds the remote id
// and no key. The cloud kinds are chat only and use the passthrough mode of
// the cloud-proxy backend, as the cloud proxy documentation shows.
func buildEntry(t target, name, remoteID string, m UpstreamModel) ([]byte, error) {
	f := entryFile{
		Name: name,
		Proxy: entryProxy{
			UpstreamURL:   t.entryUpstreamURL(),
			UpstreamModel: m.ID, // always set: an empty value would send the config name
			Remote:        remoteID,
		},
	}
	switch t.kind {
	case KindLocalAI:
		f.Backend = model.LocalAIProxyBackend
		f.KnownUsecases = validUsecases(m.Usecases)
	case KindOpenAI:
		f.Backend = model.CloudProxyBackend
		f.KnownUsecases = []string{config.UsecaseChat}
		f.Proxy.Mode = config.ProxyModePassthrough
		f.Proxy.Provider = config.ProxyProviderOpenAI
	case KindAnthropic:
		f.Backend = model.CloudProxyBackend
		f.KnownUsecases = []string{config.UsecaseChat}
		f.Proxy.Mode = config.ProxyModePassthrough
		f.Proxy.Provider = config.ProxyProviderAnthropic
	}
	body, err := yaml.Marshal(f)
	if err != nil {
		return nil, err
	}
	return append([]byte(entryHeader), body...), nil
}

func hashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
