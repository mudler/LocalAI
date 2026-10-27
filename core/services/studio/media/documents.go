package media

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/nib/extraction"

	"github.com/mudler/LocalAI/core/services/studio/session"
)

// Limits of document reading.
const (
	maxDocumentBytes = 50 << 20
	// inlineDocumentChars is how much of an attached document goes into the
	// message. The model reads the rest with read_document.
	inlineDocumentChars = 20000
	// readDocumentChars is the default size of one read_document answer.
	readDocumentChars = 20000
	extractTimeout    = 60 * time.Second
)

var documentExts = map[string]bool{".pdf": true, ".docx": true, ".xlsx": true, ".pptx": true, ".epub": true}

// IsDocumentName reports whether the file is a document whose text can be read:
// PDF, Word, Excel, PowerPoint or EPUB.
func IsDocumentName(name string) bool { return documentExts[strings.ToLower(path.Ext(name))] }

// DocumentText returns the text of a document, page by page in order. It needs
// no helper model. A scanned document with no text layer gives an empty string.
func DocumentText(ctx context.Context, name string, data []byte) (string, error) {
	if !IsDocumentName(name) {
		return "", fmt.Errorf("%s is not a document I can read", name)
	}
	if len(data) > maxDocumentBytes {
		return "", fmt.Errorf("%s is %d bytes, which is too large", name, len(data))
	}
	// The extractor reads a file, not a stream.
	dir, err := os.MkdirTemp("", "studio-doc-")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	file := filepath.Join(dir, "document"+strings.ToLower(path.Ext(name)))
	if err := os.WriteFile(file, data, 0o600); err != nil {
		return "", err
	}
	type result struct {
		text string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		text, err := extraction.ExtractText(file)
		done <- result{text, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			return "", fmt.Errorf("could not read %s: %w", name, r.err)
		}
		return strings.TrimSpace(r.text), nil
	case <-ctx.Done():
		return "", ctx.Err()
	case <-time.After(extractTimeout):
		return "", fmt.Errorf("reading %s took too long", name)
	}
}

// ExtractDocuments adds the text of the documents among the attached files to
// the message, up to inlineDocumentChars each. The files themselves are kept,
// byte for byte, and the model reads the rest with read_document. A document
// that cannot be read is skipped: the file goes with the message either way.
func (s *Service) ExtractDocuments(ctx context.Context, text string, up []session.Upload) (string, []session.Upload, error) {
	out := make([]session.Upload, len(up))
	var notes strings.Builder
	for i, u := range up {
		out[i] = u
		if !IsDocumentName(u.Name) || u.Body == nil {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(u.Body, maxDocumentBytes+1))
		if err != nil {
			return text, nil, err
		}
		if len(data) > maxDocumentBytes {
			// Too large to read here: keep it whole for the file tools.
			out[i].Body = io.MultiReader(bytes.NewReader(data), u.Body)
			continue
		}
		out[i].Body = bytes.NewReader(data)
		doc, err := DocumentText(ctx, u.Name, data)
		switch {
		case err != nil:
			continue
		case doc == "":
			fmt.Fprintf(&notes, "\n\n[Document %s has no text layer. It may be a scan.]", u.Name)
		default:
			shown, more := cutChars(doc, 0, inlineDocumentChars)
			fmt.Fprintf(&notes, "\n\n[Text of %s:\n%s", u.Name, shown)
			if more {
				fmt.Fprintf(&notes, "\n... %d of %d characters shown; read the rest with read_document.", utf8.RuneCountInString(shown), utf8.RuneCountInString(doc))
			}
			notes.WriteString("]")
		}
	}
	return text + notes.String(), out, nil
}

// cutChars returns up to n runes of s from rune offset from, and whether more
// follows.
func cutChars(s string, from, n int) (string, bool) {
	runes := []rune(s)
	if from >= len(runes) {
		return "", false
	}
	end := min(from+n, len(runes))
	return string(runes[from:end]), end < len(runes)
}
