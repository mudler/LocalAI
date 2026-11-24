// SPDX-License-Identifier: MIT
package packaging

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Channel and tag namespaces. They are disjoint by construction:
//
//	latest, development      reserved aliases, moved only by their own rule
//	<semver>                 immutable release version
//	sha-<12 hex>             immutable source commit
//	branch-<label>-<hash12>  branch channels, one per full original ref
//
// A branch tag always starts with "branch-", and no reserved alias, version
// or commit tag does, so a branch name can never take over one of them.
const (
	aliasLatest      = "latest"
	aliasDevelopment = "development"
	developmentRef   = "refs/heads/master"
	branchPrefix     = "branch-"
	maxLabel         = 40
	maxRef           = 4096
)

// Decision is what an event may publish.
type Decision struct {
	Publish   bool
	Reason    string
	Immutable string   // version or commit tag, never moves
	Aliases   []string // moving tags, promoted only after signing
	Channel   string   // serialization key of the alias promotion
	SignerRef string   // ref the signing identity carries
	Version   string   // release version when the event carries one
}

// branchLabel normalizes a branch name the way metadata-action does for a
// ref tag (lower case, characters outside the tag alphabet become a dash),
// but only ever to a bounded, readable label. The label is never trusted for
// identity: the hash of the full original ref is.
func branchLabel(name string) string {
	var b strings.Builder
	dash := true
	for _, r := range strings.ToLower(name) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	label := strings.Trim(b.String(), "-")
	if len(label) > maxLabel {
		label = strings.Trim(label[:maxLabel], "-")
	}
	if label == "" {
		label = "x"
	}
	return label
}

// devVersion is the version stamped into packages built from a branch. It is
// a pre-release of 0.0.0, so it can never be taken for a release.
func devVersion(sha string) string { return "0.0.0-dev." + sha[:12] }

func branchTag(ref string) string {
	sum := sha256.Sum256([]byte(ref))
	return branchPrefix + branchLabel(strings.TrimPrefix(ref, "refs/heads/")) + "-" + hex.EncodeToString(sum[:])[:12]
}

func validRef(ref string) error {
	if ref == "" {
		return errors.New("empty ref")
	}
	if len(ref) > maxRef || !utf8.ValidString(ref) {
		return errors.New("ref is too long or not UTF-8")
	}
	for _, r := range ref {
		if r < 0x20 || r == 0x7f {
			return errors.New("ref holds a control character")
		}
	}
	return nil
}

// decide applies the event policy. Pull requests and branch pushes never
// publish, so a branch name cannot reach a registry. The default branch alone
// publishes development. A tag push prepares the immutable version and never
// moves an alias. Only a published, non-draft, stable release moves latest.
func decide(event, ref, sha string, draft, prerelease bool) (Decision, error) {
	if err := validRef(ref); err != nil {
		return Decision{}, err
	}
	if !shaRE.MatchString(sha) {
		return Decision{}, errors.New("sha must be 40 lower-case hex characters")
	}
	commit := "sha-" + sha[:12]
	switch event {
	case "pull_request", "pull_request_target":
		return Decision{Reason: "pull request validation never publishes"}, nil
	case "push":
		switch {
		case ref == developmentRef:
			return Decision{Publish: true, Reason: "default branch publishes development", Immutable: commit, Aliases: []string{aliasDevelopment}, Channel: aliasDevelopment, SignerRef: ref, Version: devVersion(sha)}, nil
		case strings.HasPrefix(ref, "refs/heads/") && len(ref) > len("refs/heads/"):
			return Decision{Reason: "branch push never publishes; only master publishes development"}, nil
		case strings.HasPrefix(ref, "refs/tags/"):
			v, err := tagVersion(ref)
			if err != nil {
				return Decision{}, err
			}
			return Decision{Publish: true, Reason: "tag push prepares the immutable version only", Immutable: v, Channel: "version-" + v, SignerRef: ref, Version: v}, nil
		}
		return Decision{}, fmt.Errorf("unsupported ref %q", ref)
	case "release":
		if draft {
			return Decision{}, errors.New("a draft release never publishes")
		}
		v, err := tagVersion(ref)
		if err != nil {
			return Decision{}, err
		}
		d := Decision{Publish: true, Reason: "published release", Immutable: v, SignerRef: ref, Version: v, Channel: "version-" + v}
		if prerelease || strings.Contains(v, "-") {
			d.Reason = "pre-release prepares the immutable version only"
			return d, nil
		}
		d.Aliases = []string{aliasLatest}
		d.Channel = aliasLatest
		d.Reason = "stable release promotes latest after complete publication"
		return d, nil
	}
	return Decision{}, fmt.Errorf("event %q is not allowed to publish", event)
}

func tagVersion(ref string) (string, error) {
	t, ok := strings.CutPrefix(ref, "refs/tags/")
	if !ok {
		return "", fmt.Errorf("%q is not a tag ref", ref)
	}
	v, ok := strings.CutPrefix(t, "v")
	if !ok || !versionRE.MatchString(v) {
		return "", fmt.Errorf("tag %q is not a v<semver> release tag", t)
	}
	return v, nil
}

func cmdChannel(args []string) error {
	fs := flag.NewFlagSet("channel", flag.ContinueOnError)
	event := fs.String("event", "", "GitHub event name")
	ref := fs.String("ref", "", "full ref of the event")
	sha := fs.String("sha", "", "source commit")
	draft := fs.Bool("draft", false, "the release is a draft")
	pre := fs.Bool("prerelease", false, "the release is marked pre-release")
	if err := fs.Parse(args); err != nil {
		return err
	}
	d, err := decide(*event, *ref, *sha, *draft, *pre)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "publish=%t\nreason=%s\nimmutable=%s\naliases=%s\nchannel=%s\nsigner_ref=%s\nversion=%s\n",
		d.Publish, d.Reason, d.Immutable, strings.Join(d.Aliases, ","), d.Channel, d.SignerRef, d.Version)
	return nil
}
