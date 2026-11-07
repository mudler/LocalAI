package studio

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mudler/LocalAI/core/services/studio/agents"
	"github.com/mudler/LocalAI/core/services/studio/proposals"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// Knowledge writes. A model can propose a text for a collection of the
// person's Knowledge. It goes through the change proposals like a memory note,
// with two differences that are on purpose: the host never applies it unasked,
// whatever the save level says (a wrong text would feed later answers of every
// chat that searches the collection, and indexing costs compute), and applying
// it adds an entry to a collection instead of writing a file of the home.

// ErrNoKnowledgeWriter means the server cannot add to collections.
var ErrNoKnowledgeWriter = errors.New("studio: this server cannot add to Knowledge")

// CollectionWriter is the part of the collections that adds and removes a text.
// A Collections value may implement it; without it the knowledge kind is not
// offered. Every call runs as the user and sees only the user's collections.
type CollectionWriter interface {
	// EmbeddingReady returns nil when the model that indexes the collections
	// can be used, or an error that says what is missing.
	EmbeddingReady(ctx context.Context) error
	// Add stores text as one entry of the collection under the file name.
	Add(ctx context.Context, user, collection, name, text string) error
	// Remove deletes the entry that Add stored under the file name.
	Remove(ctx context.Context, user, collection, name string) error
}

// Reasons a knowledge proposal is refused, in the words the model passes on.
var (
	errKnowledgeEmbedding = errors.New("the embedding model is not installed, so nothing can be added to Knowledge until an administrator installs it")
	errKnowledgeNoList    = errors.New("no collection is on this agent's list, so it cannot add to Knowledge")
)

const maxCollectionName = 128

// knowledgeWriter returns the writer of the server, or nil.
func (s *Service) knowledgeWriter() CollectionWriter {
	w, _ := s.d.Collections.(CollectionWriter)
	return w
}

// knowledgeAvailable says whether the user can add to Knowledge on this
// server: the collections run, the account may use them and the embedding
// model is there.
func (s *Service) knowledgeAvailable(ctx context.Context, user string) bool {
	w := s.knowledgeWriter()
	return w != nil && s.d.Collections.Allowed(ctx, principal(user)) && w.EmbeddingReady(ctx) == nil
}

// knowledgeBlock says why the capability to add to Knowledge is off for the
// agent of a connection, or "" with the code when it is on. The server link
// comes first: the embedding model is missing for everybody.
func (s *Service) knowledgeBlock(ctx context.Context, user, agentID string) (code, reason string) {
	w := s.knowledgeWriter()
	if w == nil || !s.d.Collections.Allowed(ctx, principal(user)) {
		return CodeServerSettingOff, "Knowledge is not available on this server for this person"
	}
	if w.EmbeddingReady(ctx) != nil {
		return CodeKnowledgeNoEmbedding, "the embedding model is not installed"
	}
	if agentID != "" && s.d.Agents != nil {
		ag, err := s.chatAgent(ctx, principal(user), agentID)
		if err == nil && len(ag.Collections) == 0 {
			return CodeKnowledgeNone, "no collection is on the list of the agent"
		}
	}
	return "", ""
}

// knowledgeWarning is the threat scan of a knowledge proposal. A text that
// reaches a collection is read back by every chat that searches it, so a secret
// in it would leak and a wrong text would mislead. It uses the same secret
// patterns as the check that holds a memory note back from "Save it and tell
// me" (secretLike). It returns a code of ChangeProposal.Warning, or "".
func knowledgeWarning(p contract.ChangeProposal) string {
	if p.Kind != contract.ChangeKnowledge {
		return ""
	}
	if secretLike.MatchString(p.Summary) || secretLike.MatchString(p.Content) {
		return contract.WarningSecret
	}
	return ""
}

func validCollectionName(n string) bool {
	if n == "" || len(n) > maxCollectionName || !utf8.ValidString(n) {
		return false
	}
	return !strings.ContainsFunc(n, unicode.IsControl)
}

// knowledgeEntryName is the file name a proposal's text gets in the collection:
// a short slug of the summary and the start of the proposal ID, so the entry
// can be found again to undo it.
func knowledgeEntryName(p contract.ChangeProposal) string {
	var b strings.Builder
	for _, r := range strings.ToLower(p.Summary) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "-"):
			b.WriteByte('-')
		}
		if b.Len() >= 40 {
			break
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "note"
	}
	id := strings.ReplaceAll(p.ID, "-", "")
	return slug + "-" + id[:min(8, len(id))] + ".md"
}

// knowledgeEntryText is what is stored: the summary as a title, then the text.
func knowledgeEntryText(p contract.ChangeProposal) string {
	return "# " + p.Summary + "\n\n" + strings.TrimSpace(p.Content) + "\n"
}

// knowledgeKind builds the knowledge kind on the collections of the service.
func (c *Changes) knowledgeKind() proposals.Kind {
	s := c.s
	return proposals.Kind{
		Name:      contract.ChangeKnowledge,
		Grant:     contract.CapKnowledgeWrite,
		Sensitive: true,
		MaxBytes:  contract.MaxKnowledgeBytes,
		Path: func(a proposals.Aim) (string, error) {
			switch {
			case a.Scope != contract.ScopeUser:
				return "", fmt.Errorf("%w: Knowledge belongs to the user: use scope user", proposals.ErrBadTarget)
			case !validCollectionName(a.Target):
				return "", fmt.Errorf("%w: the target of a knowledge change is the name of a collection (list_collections shows them)", proposals.ErrBadTarget)
			}
			return a.Target, nil
		},
		Check: func(in proposals.CheckInput) error {
			if strings.TrimSpace(in.Content) == "" {
				return errors.New("the text to add is empty")
			}
			return nil
		},
		Dest: &proposals.Destination{
			Prepare: func(ctx context.Context, in proposals.ApplyInput) error {
				w := s.knowledgeWriter()
				if w == nil {
					return ErrNoKnowledgeWriter
				}
				if !s.d.Collections.Allowed(ctx, in.Owner) {
					return ErrNoKnowledgeWriter
				}
				if err := w.EmbeddingReady(ctx); err != nil {
					return errKnowledgeEmbedding
				}
				mine, err := s.d.Collections.List(ctx, in.Owner)
				if err != nil {
					return fmt.Errorf("cannot list the collections: %v", err)
				}
				name := in.Proposal.Target
				if !set(mine)[name] {
					return fmt.Errorf("there is no collection %q", name)
				}
				if in.Proposal.Agent == "" {
					return nil
				}
				// A saved agent adds only to the collections on its own list.
				if s.d.Agents == nil {
					return errors.New("the agent cannot be read")
				}
				ag, err := s.chatAgent(ctx, in.Owner, in.Proposal.Agent)
				switch {
				case errors.Is(err, agents.ErrNotFound):
					return errors.New("the agent no longer exists")
				case err != nil:
					return fmt.Errorf("cannot read the agent: %v", err)
				case len(ag.Collections) == 0:
					return errKnowledgeNoList
				case !set(ag.Collections)[name]:
					return fmt.Errorf("the collection %q is not on this agent's list", name)
				}
				return nil
			},
			Apply: func(ctx context.Context, in proposals.ApplyInput) error {
				w := s.knowledgeWriter()
				if w == nil {
					return ErrNoKnowledgeWriter
				}
				return w.Add(ctx, in.Owner, in.Proposal.Target, knowledgeEntryName(in.Proposal), knowledgeEntryText(in.Proposal))
			},
			Undo: func(ctx context.Context, in proposals.ApplyInput) error {
				w := s.knowledgeWriter()
				if w == nil {
					return ErrNoKnowledgeWriter
				}
				return w.Remove(ctx, in.Owner, in.Proposal.Target, knowledgeEntryName(in.Proposal))
			},
		},
	}
}
