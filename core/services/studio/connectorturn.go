package studio

import (
	"context"
	"fmt"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/session"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// connectorBlock tells the model of a chat that a messaging app started where
// its answer goes. The person reads it there, not in Studio, and the host
// attaches the files a turn makes (sendFiles) and the files the model marks
// with attach_file, so the model must not say it cannot send a file or quote a
// path of the sandbox. It is a block of the turn
// prompt, not a note: a note is cut at 400 bytes, and the text of a block is
// the same at each turn, so the harness is not restarted and the transcript
// does not grow. Other chats get nothing. canWrite says whether the chat can
// make a file at all.
func (s *Service) connectorBlock(ctx context.Context, rec session.ChatRecord, canWrite bool) string {
	if rec.Origin != session.OriginConnector {
		return ""
	}
	platform := "a messaging app"
	sends, known := false, false
	if c := s.connectors; c != nil {
		if b, err := c.o.Store.GetBinding(ctx, rec.OriginID); err == nil {
			sends, known = b.SendsFiles(), true
			platform = b.Kind
			if kinds, err := c.o.Transport.Kinds(ctx); err == nil {
				if k, ok := kindFor(kinds, b); ok && k.Title != "" {
					platform = k.Title
				}
			}
		}
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "This chat is on %s. The person reads your answer there, not in Studio, and cannot open sandbox paths.", platform)
	switch {
	case !canWrite:
	case !known:
		sb.WriteString(" Never tell them a file path.")
	case !sends:
		sb.WriteString(" Files are NOT sent to them: paste short content in the answer, or say you cannot attach a file. Never tell them a file path.")
	default:
		fmt.Fprintf(&sb, " Files you make or change in a turn are attached to your answer automatically. To give them a file that already exists, call %s with its path."+
			" Say the file is attached; never say you cannot send files, and never tell them a path."+
			" Up to %d files per turn, %d MB each; documents, data, images, audio, video and zip only, no scripts or HTML. If one cannot be attached, say so.",
			attachFileName, maxFilesPerTurn, contract.MaxConnectorFileBytes>>20)
	}
	return sb.String()
}
