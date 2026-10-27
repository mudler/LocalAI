package pptx

import (
	"archive/zip"
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/mudler/LocalAI/addons/office/internal/deck"
)

// appProps writes a one-slide deck and returns docProps/app.xml from it.
func appProps(t *testing.T, application string) string {
	t.Helper()
	slides := []deck.StudioSlide{{Layout: "title", Title: "Hello"}}
	var buf bytes.Buffer
	if err := Write(&buf, slides, Options{Title: "Hello", Application: application}, nil); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if f.Name != "docProps/app.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	t.Fatal("docProps/app.xml missing")
	return ""
}

func TestApplicationIsTheCallersName(t *testing.T) {
	got := appProps(t, "Example App")
	if !strings.Contains(got, "<Application>Example App</Application>") {
		t.Fatalf("app.xml does not carry the caller's name: %s", got)
	}
}

func TestApplicationOmittedWhenEmpty(t *testing.T) {
	got := appProps(t, "")
	if strings.Contains(got, "<Application>") {
		t.Fatalf("app.xml names an application without a caller value: %s", got)
	}
}
