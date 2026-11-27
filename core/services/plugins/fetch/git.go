package fetch

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mudler/nib/plugin/claudeplugin"
)

// sizePoll is how often a running git command has its folder measured.
const sizePoll = 200 * time.Millisecond

// gitConfig is passed with -c to every git command: https only, no hooks,
// no file system monitor, no submodules, no LFS, no template folder. The
// repository is never trusted to configure anything.
func (f *Fetcher) gitConfig() []string {
	cfg := []string{
		"protocol.allow=never",
		"protocol.https.allow=always",
		"core.hooksPath=/dev/null",
		"core.fsmonitor=false",
		"core.askPass=",
		"credential.helper=",
		"init.templateDir=",
		"submodule.recurse=false",
		"fetch.recurseSubmodules=false",
		"filter.lfs.smudge=",
		"filter.lfs.process=",
		"filter.lfs.required=false",
		"advice.detachedHead=false",
		// A server that sends less than 1 KiB/s for 30 s is dropped long
		// before the timeout of the fetch.
		"http.lowSpeedLimit=1024",
		"http.lowSpeedTime=30",
	}
	if f.allowFile {
		cfg = append(cfg, "protocol.file.allow=always")
	}
	out := make([]string, 0, 2*len(cfg))
	for _, c := range cfg {
		out = append(out, "-c", c)
	}
	return out
}

// passEnv are the variables of the server's environment that git keeps:
// the search path, the proxy and the trusted certificates. Nothing else, so
// no credential, user configuration or git variable applies.
var passEnv = []string{
	"PATH", "http_proxy", "https_proxy", "no_proxy", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
	"SSL_CERT_FILE", "SSL_CERT_DIR",
}

func (f *Fetcher) gitEnv(dir string) []string {
	env := []string{
		"HOME=" + f.home(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_LFS_SKIP_SMUDGE=1",
		"GIT_PROTOCOL_FROM_USER=0",
		"GIT_ASKPASS=",
		"SSH_ASKPASS=",
		// git looks for a repository from dir upwards. The scratch folder
		// may sit inside another repository, whose configuration must
		// not apply.
		"GIT_CEILING_DIRECTORIES=" + filepath.Dir(dir),
		"LC_ALL=C",
	}
	for _, k := range passEnv {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

// home is an empty folder under the scratch root, so git finds no user
// configuration.
func (f *Fetcher) home() string { return filepath.Join(f.root, ".home") }

// Git returns the hardened claudeplugin.GitFunc of the fetcher. Every fetch
// is partial (--filter=blob:none) and shallow (--depth 1), so a sparse
// checkout downloads the blobs of its folder only, and a fallback fetch of
// a ref never pulls its history. A command whose folder grows over
// MaxScratchBytes is stopped and fails with claudeplugin.ErrTooLarge.
func (f *Fetcher) Git() claudeplugin.GitFunc {
	g := f.runGit
	if f.wrapGit != nil {
		g = f.wrapGit(g)
	}
	return g
}

func (f *Fetcher) runGit(ctx context.Context, dir string, args ...string) (string, error) {
	if len(args) == 0 {
		return "", errors.New("git: no command")
	}
	if err := os.MkdirAll(f.home(), 0o700); err != nil {
		return "", err
	}
	if dir == "" {
		// ls-remote needs no repository; run it in an empty folder.
		tmp, err := os.MkdirTemp(f.root, ".git-")
		if err != nil {
			return "", err
		}
		defer os.RemoveAll(tmp)
		dir = tmp
	}
	args = hardenArgs(args)
	full := append(f.gitConfig(), args...)

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Dir = dir
	cmd.Env = f.gitEnv(dir)
	cmd.WaitDelay = 5 * time.Second
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb

	var over atomic.Bool
	done := make(chan struct{})
	if f.maxScratch > 0 {
		go func() {
			t := time.NewTicker(sizePoll)
			defer t.Stop()
			for {
				select {
				case <-done:
					return
				case <-t.C:
					if dirSize(dir) > f.maxScratch {
						over.Store(true)
						cancel()
						return
					}
				}
			}
		}()
	}
	err := cmd.Start()
	if err == nil && f.maxScratch > 0 {
		// The poll above stops a growing folder within sizePoll; the file
		// size limit stops one file at the cap at once.
		if lerr := limitFileSize(cmd.Process.Pid, f.maxScratch); lerr != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			close(done)
			return "", fmt.Errorf("git %s: limit the file size: %w", args[0], lerr)
		}
	}
	if err == nil {
		err = cmd.Wait()
	}
	close(done)
	if killedForSize(err) {
		over.Store(true)
	}
	if over.Load() || (f.maxScratch > 0 && dirSize(dir) > f.maxScratch) {
		return "", fmt.Errorf("git %s: %w: the checkout is over %d bytes", args[0], claudeplugin.ErrTooLarge, f.maxScratch)
	}
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, firstLine(errb.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

// hardenArgs makes every fetch partial and shallow, and never recursive.
// nib's checkout fetches without a blob filter (gap N10) and, when a server
// refuses a fetch by SHA, fetches the full history of the ref; with --depth
// 1 that fallback stays shallow and the checkout of an older SHA fails.
func hardenArgs(args []string) []string {
	if args[0] != "fetch" {
		return args
	}
	out := []string{"fetch", "--filter=blob:none", "--no-tags", "--no-recurse-submodules"}
	hasDepth := false
	for _, a := range args[1:] {
		if a == "--depth" || strings.HasPrefix(a, "--depth=") {
			hasDepth = true
		}
	}
	if !hasDepth {
		out = append(out, "--depth", "1")
	}
	return append(out, args[1:]...)
}

// dirSize is the size of the regular files under dir. A folder that
// changes while it is read gives a best effort count.
func dirSize(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

// firstLine keeps the first line of git's error output, cut to 200 bytes.
// The rest may echo a URL or a path and adds nothing for the caller.
func firstLine(s string) string {
	s = strings.TrimSpace(s)
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "warning:") {
			continue
		}
		if len(l) > 200 {
			l = l[:200]
		}
		return l
	}
	return ""
}
