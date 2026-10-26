package workspace_test

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/core/services/studio/workspace"
)

func tarOf(t *testing.T, build func(w *tar.Writer)) io.Reader {
	t.Helper()
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	build(w)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return &buf
}

func file(w *tar.Writer, name, body string) {
	_ = w.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(body))})
	_, _ = w.Write([]byte(body))
}

type got struct {
	e    workspace.UpperEntry
	body string
}

func walk(t *testing.T, r io.Reader) ([]got, error) {
	t.Helper()
	var out []got
	err := workspace.ArchiveSource(r).Walk(context.Background(), func(e workspace.UpperEntry) error {
		g := got{e: e}
		if e.Kind == workspace.EntryFile {
			rc, err := e.Open()
			if err != nil {
				return err
			}
			b, _ := io.ReadAll(rc)
			_ = rc.Close()
			g.body = string(b)
		}
		out = append(out, g)
		return nil
	})
	return out, err
}

func TestAnArchiveIsAnUpperLayerWithAllItsEntryKinds(t *testing.T) {
	r := tarOf(t, func(w *tar.Writer) {
		_ = w.WriteHeader(&tar.Header{Name: "home/", Typeflag: tar.TypeDir, Mode: 0o755})
		file(w, "home/a.txt", "alpha")
		_ = w.WriteHeader(&tar.Header{Name: "home/opaque/", Typeflag: tar.TypeDir, Mode: 0o755,
			Format: tar.FormatPAX, PAXRecords: map[string]string{"SCHILY.xattr.trusted.overlay.opaque": "y", "SCHILY.xattr.security.selinux": "x"}})
		_ = w.WriteHeader(&tar.Header{Name: "home/gone.txt", Typeflag: tar.TypeChar, Devmajor: 0, Devminor: 0})
		_ = w.WriteHeader(&tar.Header{Name: "home/link", Typeflag: tar.TypeSymlink, Linkname: "a.txt"})
		_ = w.WriteHeader(&tar.Header{Name: "home/null", Typeflag: tar.TypeChar, Devmajor: 1, Devminor: 3})
	})
	es, err := walk(t, r)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]workspace.EntryKind{}
	for _, g := range es {
		kinds[g.e.Path] = g.e.Kind
	}
	want := map[string]workspace.EntryKind{
		"home": workspace.EntryDir, "home/a.txt": workspace.EntryFile, "home/opaque": workspace.EntryDir,
		"home/gone.txt": workspace.EntryWhiteout, "home/link": workspace.EntrySymlink, "home/null": workspace.EntryOther,
	}
	for p, k := range want {
		if kinds[p] != k {
			t.Errorf("entry %s has kind %v, want %v", p, kinds[p], k)
		}
	}
	for _, g := range es {
		switch g.e.Path {
		case "home/a.txt":
			if g.body != "alpha" {
				t.Errorf("a.txt = %q", g.body)
			}
		case "home/opaque":
			if string(g.e.Xattrs["trusted.overlay.opaque"]) != "y" {
				t.Errorf("the opaque attribute was lost: %v", g.e.Xattrs)
			}
			if _, ok := g.e.Xattrs["security.selinux"]; ok {
				t.Errorf("a non-overlay attribute was passed on: %v", g.e.Xattrs)
			}
		}
	}
}

func TestAnArchiveCannotNameAPathOutsideTheLayer(t *testing.T) {
	for _, name := range []string{"../escape.txt", "/abs.txt", "a/../../b", "a//b/../../../c"} {
		r := tarOf(t, func(w *tar.Writer) { file(w, name, "x") })
		if _, err := walk(t, r); err == nil {
			t.Errorf("an entry named %q was accepted", name)
		}
	}
}

func TestAnArchiveOverTheLimitsFailsWholesale(t *testing.T) {
	deep := strings.Repeat("d/", 513) + "f.txt"
	if _, err := walk(t, tarOf(t, func(w *tar.Writer) { file(w, deep, "x") })); err == nil || !strings.Contains(err.Error(), "512") {
		t.Errorf("a path 513 levels deep = %v, want an error naming 512", err)
	}
	defer workspace.SetArchiveMaxEntries(1000)()
	var many bytes.Buffer
	w := tar.NewWriter(&many)
	for i := 0; i < 1001; i++ {
		_ = w.WriteHeader(&tar.Header{Name: fmt.Sprintf("f%d", i), Typeflag: tar.TypeReg, Mode: 0o644})
	}
	_ = w.Close()
	if _, err := walk(t, &many); err == nil || !strings.Contains(err.Error(), "1000 entries") {
		t.Errorf("an archive over the entry limit = %v, want an error naming the limit", err)
	}
}

func TestAnArchiveIsReadOnce(t *testing.T) {
	src := workspace.ArchiveSource(tarOf(t, func(w *tar.Writer) { file(w, "a", "x") }))
	if err := src.Walk(context.Background(), func(workspace.UpperEntry) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := src.Walk(context.Background(), func(workspace.UpperEntry) error { return nil }); err == nil {
		t.Fatal("a second walk of a stream succeeded")
	}
}
