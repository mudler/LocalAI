// SPDX-License-Identifier: MIT
package packaging

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/layout"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
)

var (
	immutableTagRE = regexp.MustCompile(`^(sha-[0-9a-f]{12}|(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z]+(\.[0-9A-Za-z-]+)*)?)$`)
	branchAliasRE  = regexp.MustCompile(`^branch-[a-z0-9]+(-[a-z0-9]+)*-[0-9a-f]{12}$`)
)

// backendRepositoryPrefixes are repositories that belong to the backend
// image pipeline. Add-on packages never go there, whatever the input says.
var backendRepositoryPrefixes = []string{
	"quay.io/go-skynet/",
	"index.docker.io/localai/",
	"docker.io/localai/",
	"ghcr.io/mudler/localai",
}

// addonRepositoryNamespace is the one repository under the backend registry
// organization that belongs to add-on packages. It is the only exception to
// backendRepositoryPrefixes.
const addonRepositoryNamespace = "quay.io/go-skynet/local-ai-addons"

// parseRepository validates the registry and repository input. It has no
// default: an empty value is an error, so a missing configuration variable
// fails the run instead of publishing somewhere unintended.
func parseRepository(s string) (name.Repository, error) {
	if strings.TrimSpace(s) == "" {
		return name.Repository{}, errors.New("repository input is required: set the add-on registry and repository configuration")
	}
	if strings.ContainsAny(s, "@: ") && !strings.Contains(s, ":") {
		return name.Repository{}, errors.New("repository must not carry a tag or digest")
	}
	repo, err := name.NewRepository(s, name.StrictValidation)
	if err != nil {
		return name.Repository{}, fmt.Errorf("repository %q: %w", s, err)
	}
	full := strings.ToLower(repo.Name())
	if full == addonRepositoryNamespace || strings.HasPrefix(full, addonRepositoryNamespace+"/") {
		return repo, nil
	}
	for _, p := range backendRepositoryPrefixes {
		if strings.HasPrefix(full+"/", p) || strings.HasPrefix(full, p) {
			return name.Repository{}, fmt.Errorf("repository %q is a backend image repository; add-on packages use their own namespace", s)
		}
	}
	return repo, nil
}

func isNotFound(err error) bool {
	var te *transport.Error
	return errors.As(err, &te) && te.StatusCode == 404
}

func remoteOptions(ctx context.Context) []remote.Option {
	return []remote.Option{remote.WithContext(ctx), remote.WithAuthFromKeychain(authn.DefaultKeychain)}
}

// guardImmutable refuses a tag that already names different content.
func guardImmutable(ctx context.Context, tag name.Tag, digest v1.Hash) (exists bool, err error) {
	d, err := remote.Head(tag, remoteOptions(ctx)...)
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("reading %s: %w", tag, err)
	}
	if d.Digest != digest {
		return true, fail("E_IMMUTABLE", "%s already names %s; an immutable tag is never overwritten with %s", tag.Identifier(), d.Digest, digest)
	}
	return true, nil
}

// publishIndex pushes a verified merged layout: every platform image and the
// index by digest first, then the immutable tag. Re-running it with the same
// layout resumes: pushes of existing blobs are no-ops and the tag already
// names the same digest.
func publishIndex(ctx context.Context, dir string, cfg *PlatformsFile, repo name.Repository, immutable string, adopt bool) (v1.Hash, error) {
	var zero v1.Hash
	if !immutableTagRE.MatchString(immutable) {
		return zero, fail("E_RESERVED", "%q is not an immutable version or commit tag", immutable)
	}
	m, digest, err := verifyMerged(dir, cfg)
	if err != nil {
		return zero, err
	}
	if strings.HasPrefix(immutable, "sha-") {
		if len(m.Revision) != 40 || immutable != "sha-"+m.Revision[:12] {
			return zero, fail("E_RESERVED", "commit tag %q does not name the source commit of the layout", immutable)
		}
	} else if immutable != m.Version {
		return zero, fail("E_RESERVED", "version tag %q differs from the layout version %q", immutable, m.Version)
	}
	tag := repo.Tag(immutable)
	if _, err := guardImmutable(ctx, tag, digest); err != nil {
		if adopt && strings.HasPrefix(err.Error(), "E_IMMUTABLE") {
			// A rebuild of the same version and commit differs in bytes only
			// when the build is not reproducible. The published content is
			// kept, never replaced, after it passes the same completeness
			// rules; the workflow verifies its signature next.
			if d, aerr := adoptExisting(ctx, repo, tag, m, cfg); aerr == nil {
				return d, nil
			} else {
				return zero, fmt.Errorf("%w; cannot adopt: %v", err, aerr)
			}
		}
		return zero, err
	}
	lp, err := layout.FromPath(dir)
	if err != nil {
		return zero, err
	}
	root, err := lp.ImageIndex()
	if err != nil {
		return zero, err
	}
	idx, err := root.ImageIndex(digest)
	if err != nil {
		return zero, err
	}
	opts := remoteOptions(ctx)
	if err := remote.WriteIndex(repo.Digest(digest.String()), idx, opts...); err != nil {
		return zero, fmt.Errorf("pushing %s: %w", digest, err)
	}
	// A second look closes the window between the first check and the tag.
	if _, err := guardImmutable(ctx, tag, digest); err != nil {
		return zero, err
	}
	if err := remote.Tag(tag, idx, opts...); err != nil {
		return zero, fmt.Errorf("tagging %s: %w", tag, err)
	}
	return digest, nil
}

// adoptExisting accepts the content that an immutable tag already names when
// it is a complete package index of the same add-on, version and commit.
func adoptExisting(ctx context.Context, repo name.Repository, tag name.Tag, m *merged, cfg *PlatformsFile) (v1.Hash, error) {
	var zero v1.Hash
	d, err := remote.Get(tag, remoteOptions(ctx)...)
	if err != nil {
		return zero, err
	}
	idx, err := d.ImageIndex()
	if err != nil {
		return zero, errors.New("existing tag does not name an image index")
	}
	im, err := idx.IndexManifest()
	if err != nil {
		return zero, err
	}
	a := im.Annotations
	if im.ArtifactType != IndexArtifactType || a[AnnotationID] != cfg.Addon || a[AnnotationVersion] != m.Version || a[AnnotationRevision] != m.Revision || m.Revision == "" {
		return zero, errors.New("existing index was not built from the same add-on, version and commit")
	}
	enabled, err := cfg.enabled()
	if err != nil {
		return zero, err
	}
	have := map[string]bool{}
	for _, c := range im.Manifests {
		if c.Platform != nil {
			have[c.Platform.OS+"/"+c.Platform.Architecture] = true
		}
	}
	if len(have) != len(enabled) || len(im.Manifests) != len(enabled) {
		return zero, errors.New("existing index does not match the enabled platforms")
	}
	for _, e := range enabled {
		if !have[e.Platform] {
			return zero, fmt.Errorf("existing index has no %s image", e.Platform)
		}
	}
	return d.Digest, nil
}

func cmdPublish(args []string) error {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	dir := fs.String("layout", "", "merged layout")
	repository := fs.String("repository", "", "registry and repository, for example registry.example/owner/addon-tables")
	immutable := fs.String("tag", "", "immutable version or commit tag")
	config := fs.String("config", "", "platform matrix file")
	adopt := fs.Bool("adopt-existing", false, "keep a published index of the same version and commit when a rebuild differs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	repo, err := parseRepository(*repository)
	if err != nil {
		return err
	}
	cfg, err := loadPlatforms(*config)
	if err != nil {
		return err
	}
	if *dir == "" {
		return errors.New("--layout is required")
	}
	ctx := context.Background()
	d, err := publishIndex(ctx, *dir, cfg, repo, *immutable, *adopt)
	if err != nil {
		return err
	}
	children, err := childDigests(ctx, repo, d)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "digest=%s\nreference=%s@%s\nchildren=%s\n", d, repo.Name(), d, strings.Join(children, ","))
	return nil
}

// Promotion outcomes.
const (
	promoted  = "promoted"
	unchanged = "unchanged"
	stale     = "stale"
)

type aliasState struct {
	digest   v1.Hash
	version  string
	revision string
}

func readAlias(ctx context.Context, repo name.Repository, alias string) (*aliasState, error) {
	ref := repo.Tag(alias)
	d, err := remote.Get(ref, remoteOptions(ctx)...)
	if err != nil {
		if isNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	st := &aliasState{digest: d.Digest}
	idx, err := d.ImageIndex()
	if err != nil {
		return nil, fail("E_STALE", "alias %s does not name an image index", alias)
	}
	im, err := idx.IndexManifest()
	if err != nil {
		return nil, err
	}
	st.version, st.revision = im.Annotations[AnnotationVersion], im.Annotations[AnnotationRevision]
	return st, nil
}

func compareSemver(a, b string) int {
	pa, pb := splitSemver(a), splitSemver(b)
	for i := 0; i < 3; i++ {
		if pa.num[i] != pb.num[i] {
			if pa.num[i] < pb.num[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case pa.pre == "" && pb.pre == "":
		return 0
	case pa.pre == "":
		return 1
	case pb.pre == "":
		return -1
	}
	as, bs := strings.Split(pa.pre, "."), strings.Split(pb.pre, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] == bs[i] {
			continue
		}
		an, aok := atoi(as[i])
		bn, bok := atoi(bs[i])
		switch {
		case aok && bok:
			if an < bn {
				return -1
			}
			return 1
		case aok:
			return -1
		case bok:
			return 1
		case as[i] < bs[i]:
			return -1
		default:
			return 1
		}
	}
	return len(as) - len(bs)
}

type semver struct {
	num [3]int
	pre string
}

func splitSemver(v string) semver {
	core, pre, _ := strings.Cut(v, "-")
	var s semver
	for i, p := range strings.SplitN(core, ".", 3) {
		n, _ := atoi(p)
		if i < 3 {
			s.num[i] = n
		}
	}
	s.pre = pre
	return s
}

func atoi(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' || n > 1<<40 {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}

// errCommitUnreachable reports that git does not have one of the commits.
// It is not the same as "not an ancestor": the history that would answer the
// question is missing from the checkout.
var errCommitUnreachable = errors.New("commit is not reachable in this checkout")

// isAncestor asks git whether candidate (the commit of this run) is an
// ancestor of named (the commit an alias names). It returns
// errCommitUnreachable when named is unknown to the repository, as happens
// after a force-push. A missing candidate and any other git failure are plain
// errors.
func isAncestor(ctx context.Context, gitDir, candidate, named string) (bool, error) {
	if !shaRE.MatchString(candidate) || !shaRE.MatchString(named) {
		return false, errors.New("commits must be 40 hex characters")
	}
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "git", "--git-dir", gitDir, "merge-base", "--is-ancestor", candidate, named) // #nosec G204 -- fixed git arguments; the revisions are 40-hex commit identifiers checked by the caller
	cmd.Stderr = &stderr
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return true, nil
	case errors.As(err, &ee) && ee.ExitCode() == 1:
		return false, nil
	case errors.As(err, &ee) && ee.ExitCode() == 128 && strings.Contains(stderr.String(), "Not a valid commit name"):
		// Which side is missing? This run's commit must be in the checkout.
		if !commitExists(ctx, gitDir, candidate) {
			return false, fmt.Errorf("git merge-base: commit %s of this run is missing: %w", candidate, err)
		}
		return false, fmt.Errorf("%w: %s", errCommitUnreachable, named)
	}
	return false, fmt.Errorf("git merge-base: %w: %s", err, strings.TrimSpace(stderr.String()))
}

func commitExists(ctx context.Context, gitDir, sha string) bool {
	return exec.CommandContext(ctx, "git", "--git-dir", gitDir, "cat-file", "-e", sha+"^{commit}").Run() == nil // #nosec G204 -- fixed git arguments; the revisions are 40-hex commit identifiers checked by the caller
}

// promotionSource says which event asks for the promotion.
type promotionSource struct {
	Ref   string // full git ref of the event, for example refs/heads/feature/x
	Event string // GitHub event name
}

// pushToOwnBranch reports whether the event is a push to the ref that owns
// this branch channel. The channel name is derived from the ref, so the name
// itself is the record of which ref the channel follows.
func (s promotionSource) pushToOwnBranch(alias string) bool {
	return s.Event == "push" && strings.HasPrefix(s.Ref, "refs/heads/") && branchTag(s.Ref) == alias
}

// promoteAlias moves one moving tag to a digest that was already published,
// verified and signed. It never moves an alias backwards: a slower, older run
// finds a newer alias and leaves it alone.
func promoteAlias(ctx context.Context, repo name.Repository, alias string, digest v1.Hash, version, revision, gitDir string) (string, error) {
	return promoteAliasFrom(ctx, repo, alias, digest, version, revision, gitDir, promotionSource{})
}

// promoteAliasFrom is promoteAlias with the event that asks for the move. A
// branch channel whose current commit is gone from the checkout (the branch
// was force-pushed) accepts only a push to its own ref as the new truth.
func promoteAliasFrom(ctx context.Context, repo name.Repository, alias string, digest v1.Hash, version, revision, gitDir string, src promotionSource) (string, error) {
	switch {
	case alias == aliasLatest:
		if !versionRE.MatchString(version) || strings.Contains(version, "-") {
			return "", fail("E_RESERVED", "latest only follows a stable release version, got %q", version)
		}
	case alias == aliasDevelopment || branchAliasRE.MatchString(alias):
		if !shaRE.MatchString(revision) {
			return "", fail("E_RESERVED", "alias %s needs the source commit", alias)
		}
	default:
		return "", fail("E_RESERVED", "%q is not a channel alias", alias)
	}
	cur, err := readAlias(ctx, repo, alias)
	if err != nil {
		return "", err
	}
	// The target must already exist in the registry: a promotion never
	// creates content, it only names content that was published and signed.
	target, err := remote.Get(repo.Digest(digest.String()), remoteOptions(ctx)...)
	if err != nil {
		return "", fmt.Errorf("target %s is not published: %w", digest, err)
	}
	if cur != nil {
		if cur.digest == digest {
			return unchanged, nil
		}
		if alias == aliasLatest {
			if cur.version != "" && version != "" {
				switch c := compareSemver(version, cur.version); {
				case c < 0:
					return stale, nil
				case c == 0:
					return "", fail("E_IMMUTABLE", "latest already names version %s with another digest %s", cur.version, cur.digest)
				}
			}
		} else if cur.revision != "" {
			if cur.revision == revision {
				return "", fail("E_IMMUTABLE", "channel %s already names commit %s with another digest %s", alias, revision, cur.digest)
			}
			// The new commit is older than the one the alias names: a slower
			// run. A descendant, or a diverged commit, is the new truth of
			// the branch. When the named commit is gone from the checkout
			// the branch was rewritten: only a push to the channel's own
			// branch may take it over. Everything else fails closed.
			older, err := isAncestor(ctx, gitDir, revision, cur.revision)
			switch {
			case errors.Is(err, errCommitUnreachable):
				if alias == aliasDevelopment || !src.pushToOwnBranch(alias) {
					return "", fail("E_UNREACHABLE", "channel %s names commit %s, which is not in this checkout; only a push to the channel's own branch may replace it", alias, cur.revision)
				}
				older = false
			case err != nil:
				return "", err
			}
			if older {
				return stale, nil
			}
		}
	}
	idx, err := target.ImageIndex()
	if err != nil {
		return "", err
	}
	if err := remote.Tag(repo.Tag(alias), idx, remoteOptions(ctx)...); err != nil {
		return "", fmt.Errorf("tagging %s: %w", alias, err)
	}
	return promoted, nil
}

func cmdPromote(args []string) error {
	fs := flag.NewFlagSet("promote", flag.ContinueOnError)
	repository := fs.String("repository", "", "registry and repository")
	alias := fs.String("alias", "", "latest, development or a branch channel tag")
	digest := fs.String("digest", "", "published index digest")
	version := fs.String("version", "", "release version of the digest")
	revision := fs.String("revision", "", "source commit of the digest")
	gitDir := fs.String("git-dir", ".git", "git directory with the history of both commits")
	ref := fs.String("ref", "", "git ref of the event that asks for the promotion")
	event := fs.String("event", "", "GitHub event name that asks for the promotion")
	if err := fs.Parse(args); err != nil {
		return err
	}
	repo, err := parseRepository(*repository)
	if err != nil {
		return err
	}
	if !digestRE.MatchString(*digest) {
		return errors.New("--digest must be sha256:<64 hex>")
	}
	h, err := v1.NewHash(*digest)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err := os.Stat(*gitDir); err != nil && *alias != aliasLatest {
		return fmt.Errorf("git directory: %w", err)
	}
	out, err := promoteAliasFrom(ctx, repo, *alias, h, *version, *revision, *gitDir, promotionSource{Ref: *ref, Event: *event})
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "result=%s\nalias=%s\ndigest=%s\n", out, *alias, h)
	return nil
}

// childDigests lists the platform images of a published index, so the
// workflow can verify each one the way an installer will.
func childDigests(ctx context.Context, repo name.Repository, digest v1.Hash) ([]string, error) {
	d, err := remote.Get(repo.Digest(digest.String()), remoteOptions(ctx)...)
	if err != nil {
		return nil, err
	}
	idx, err := d.ImageIndex()
	if err != nil {
		return nil, err
	}
	im, err := idx.IndexManifest()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, c := range im.Manifests {
		out = append(out, c.Digest.String())
	}
	return out, nil
}
