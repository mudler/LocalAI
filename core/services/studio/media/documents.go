package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/extract"
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

// IsDocumentName reports whether the file is a document whose text can be read:
// PDF, Word, Excel, PowerPoint or EPUB.
func IsDocumentName(name string) bool { return extract.Supported(name) }

// DocumentText returns the text of a document: the sheets of a workbook as
// tab-separated rows, the headings, paragraphs and tables of a Word document,
// the slides of a presentation, the pages of a PDF or an EPUB. It needs no
// helper model. A PDF with no text layer gives extract.ErrNoText; an Office
// file with no text gives an empty string.
func DocumentText(ctx context.Context, name string, data []byte) (string, error) {
	if !IsDocumentName(name) {
		return "", fmt.Errorf("%s is not a document I can read", name)
	}
	if len(data) > maxDocumentBytes {
		return "", fmt.Errorf("%s is %d bytes, which is too large", name, len(data))
	}
	ctx, cancel := context.WithTimeout(ctx, extractTimeout)
	defer cancel()
	text, _, err := extract.Extract(ctx, name, data)
	switch {
	case errors.Is(err, extract.ErrNoText):
		return "", err
	case errors.Is(err, extract.ErrInvalid):
		return "", fmt.Errorf("%s is not a valid %s file", name, strings.TrimPrefix(strings.ToLower(path.Ext(name)), "."))
	case errors.Is(err, context.DeadlineExceeded):
		return "", fmt.Errorf("reading %s took too long", name)
	case err != nil:
		return "", fmt.Errorf("could not read %s: %w", name, err)
	}
	return strings.TrimSpace(text), nil
}

// documentProblem is what the model is told about a document with no text: a
// PDF with no text layer is a scan, an Office file is empty.
func documentProblem(name string, err error) string {
	if errors.Is(err, extract.ErrNoText) {
		return fmt.Sprintf("%s is a PDF with no text layer: it is probably a scan, so its words are pictures. describe_image reads an image of a page", name)
	}
	return fmt.Sprintf("%s contains no text (its sheets, pages or slides are empty)", name)
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
		case errors.Is(err, extract.ErrNoText):
			fmt.Fprintf(&notes, "\n\n[Document %s has no text layer. It may be a scan.]", u.Name)
		case err != nil:
			continue
		case doc == "":
			fmt.Fprintf(&notes, "\n\n[Document %s contains no text.]", u.Name)
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
