// SPDX-License-Identifier: MIT
package imapc

import (
	"context"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/mudler/LocalAI/addons/mail/internal/mimetext"
)

// Limits of what one message read fetches.
const (
	// MaxRawBodyPart is the most bytes fetched of one text part.
	MaxRawBodyPart = 256 << 10
	// MaxRawAttachment is the largest part read-attachment fetches.
	MaxRawAttachment = 8 << 20
)

// Attachment describes a part without its bytes.
type Attachment struct {
	Part string `json:"part"`
	Name string `json:"name,omitempty"`
	Type string `json:"type"`
	Size int64  `json:"size"`
	// Text tells that read-attachment can show it.
	Text bool `json:"text"`
}

// Message is one message with its text.
type Message struct {
	UID        uint32
	MessageID  string
	Subject    string
	From       []Addr
	To         []Addr
	Cc         []Addr
	Date       time.Time
	InReplyTo  []string
	References []string
	Unseen     bool
	// Body is the text, cleaned. BodyBytes is its size before any cut by the
	// caller; Converted tells that it came from HTML; PartCut tells that a
	// part was larger than the fetch limit.
	Body        string
	Converted   bool
	PartCut     bool
	Attachments []Attachment
}

func (s *Session) fetchOne(ctx context.Context, uid uint32, o *imap.FetchOptions) (bufUID imap.UID, buf interface {
	FindBodySection(*imap.FetchItemBodySection) []byte
}, env *imap.Envelope, flags []imap.Flag, bs imap.BodyStructure, err error) {
	var set imap.UIDSet
	set.AddNum(imap.UID(uid))
	o.UID = true
	bufs, err := s.c.Fetch(set, o).Collect()
	if err != nil {
		return 0, nil, nil, nil, nil, mapErr(ctx, err, ErrServer)
	}
	if len(bufs) == 0 || uint32(bufs[0].UID) != uid {
		return 0, nil, nil, nil, nil, ErrNoMessage
	}
	b := bufs[0]
	return b.UID, b, b.Envelope, b.Flags, b.BodyStructure, nil
}

// Get reads one message: headers, text, and the list of attachments. Every
// fetch uses PEEK, so the message keeps its flags.
func (s *Session) Get(ctx context.Context, folder string, uid uint32) (Message, error) {
	if err := s.selectRO(ctx, folder); err != nil {
		return Message{}, err
	}
	refs := &imap.FetchItemBodySection{Specifier: imap.PartSpecifierHeader, HeaderFields: []string{"References"}, Peek: true}
	_, buf, env, flags, bs, err := s.fetchOne(ctx, uid, &imap.FetchOptions{Envelope: true, Flags: true, BodyStructure: &imap.FetchItemBodyStructure{}, BodySection: []*imap.FetchItemBodySection{refs}})
	if err != nil {
		return Message{}, err
	}
	m := Message{UID: uid, Unseen: true}
	for _, f := range flags {
		if f == imap.FlagSeen {
			m.Unseen = false
		}
	}
	if env != nil {
		m.MessageID, m.Subject, m.Date, m.InReplyTo = env.MessageID, env.Subject, env.Date, env.InReplyTo
		m.From, m.To, m.Cc = addrs(env.From), addrs(env.To), addrs(env.Cc)
	}
	m.References = parseReferences(buf.FindBodySection(refs))
	if bs == nil {
		return m, nil
	}
	all := Parts(bs)
	for _, p := range all {
		if p.Role == RoleAttachment {
			m.Attachments = append(m.Attachments, Attachment{Part: p.PathString(), Name: p.Name, Type: p.Type, Size: p.DecodedSize(), Text: p.Text() && !p.IsMessage})
		}
	}
	body, isHTML := BodyParts(all)
	if len(body) == 0 {
		return m, nil
	}
	var items []*imap.FetchItemBodySection
	for _, p := range body {
		items = append(items, &imap.FetchItemBodySection{Part: p.Path, Peek: true, Partial: &imap.SectionPartial{Offset: 0, Size: MaxRawBodyPart}})
		if p.Size > MaxRawBodyPart {
			m.PartCut = true
		}
	}
	_, textBuf, _, _, _, err := s.fetchOne(ctx, uid, &imap.FetchOptions{BodySection: items})
	if err != nil {
		return Message{}, err
	}
	var texts []string
	for i, p := range body {
		raw := textBuf.FindBodySection(items[i])
		t := mimetext.ToUTF8(p.Charset, mimetext.DecodeTransfer(p.Encoding, raw))
		if isHTML {
			t = mimetext.HTMLText(t)
		}
		if t = mimetext.Clean(t); t != "" {
			texts = append(texts, t)
		}
	}
	m.Body, m.Converted = strings.Join(texts, "\n\n"), isHTML
	return m, nil
}

func parseReferences(raw []byte) []string {
	s := string(raw)
	if i := strings.Index(strings.ToLower(s), "references:"); i >= 0 {
		s = s[i+len("references:"):]
	}
	var out []string
	for _, f := range strings.Fields(s) {
		f = strings.Trim(f, "<>")
		if f != "" && len(f) <= 255 && !strings.ContainsAny(f, "<>\r\n") {
			out = append(out, f)
		}
	}
	if len(out) > 20 {
		out = out[len(out)-20:]
	}
	return out
}

// PartText is the readable text of one attachment part.
type PartText struct {
	Name, Type string
	Size       int64
	// Text is set for a text type; Binary says the type is not shown.
	Text      string
	Binary    bool
	Converted bool
}

// ReadPart fetches one attachment and decodes it when it is text. For any
// other type it returns the details alone and fetches nothing.
func (s *Session) ReadPart(ctx context.Context, folder string, uid uint32, part string) (PartText, error) {
	if err := s.selectRO(ctx, folder); err != nil {
		return PartText{}, err
	}
	_, _, _, _, bs, err := s.fetchOne(ctx, uid, &imap.FetchOptions{BodyStructure: &imap.FetchItemBodyStructure{}})
	if err != nil {
		return PartText{}, err
	}
	if bs == nil {
		return PartText{}, ErrNoPart
	}
	for _, p := range Parts(bs) {
		if p.PathString() != part || p.Role != RoleAttachment {
			continue
		}
		out := PartText{Name: p.Name, Type: p.Type, Size: p.DecodedSize()}
		if !p.Text() || p.IsMessage {
			out.Binary = true
			return out, nil
		}
		if p.Size > MaxRawAttachment {
			return PartText{}, ErrTooLarge
		}
		sec := &imap.FetchItemBodySection{Part: p.Path, Peek: true}
		_, buf, _, _, _, err := s.fetchOne(ctx, uid, &imap.FetchOptions{BodySection: []*imap.FetchItemBodySection{sec}})
		if err != nil {
			return PartText{}, err
		}
		t := mimetext.ToUTF8(p.Charset, mimetext.DecodeTransfer(p.Encoding, buf.FindBodySection(sec)))
		if p.Type == "text/html" {
			t, out.Converted = mimetext.HTMLText(t), true
		}
		out.Text = mimetext.Clean(t)
		return out, nil
	}
	return PartText{}, ErrNoPart
}

// Thread returns the messages of the folder that belong to the conversation of
// a message: the ones that name its Message-ID or one of the IDs it refers to,
// and the ones it refers to. The search is inside one folder.
func (s *Session) Thread(ctx context.Context, folder string, uid uint32, limit int) ([]Summary, error) {
	m, err := s.Get(ctx, folder, uid)
	if err != nil {
		return nil, err
	}
	ids := map[string]bool{}
	for _, id := range append(append([]string{m.MessageID}, m.InReplyTo...), m.References...) {
		if id = strings.Trim(id, "<>"); id != "" {
			ids[id] = true
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	var crit []imap.SearchCriteria
	for id := range ids {
		for _, h := range []string{"Message-ID", "References", "In-Reply-To"} {
			crit = append(crit, imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: h, Value: id}}})
		}
	}
	if len(crit) > 60 {
		crit = crit[:60]
	}
	root := crit[len(crit)-1]
	for i := len(crit) - 2; i >= 0; i-- {
		root = imap.SearchCriteria{Or: [][2]imap.SearchCriteria{{crit[i], root}}}
	}
	data, err := s.c.UIDSearch(&root, nil).Wait()
	if err != nil {
		return nil, mapErr(ctx, err, ErrServer)
	}
	uids := data.AllUIDs()
	if len(uids) > limit {
		uids = uids[len(uids)-limit:]
	}
	if len(uids) == 0 {
		return nil, nil
	}
	items, err := s.summaries(ctx, uids)
	if err != nil {
		return nil, err
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items, nil
}

// AppendDraft saves a message in a folder with the \Draft flag and returns its
// UID when the server says it.
func (s *Session) AppendDraft(ctx context.Context, folder string, raw []byte) (uint32, error) {
	if err := s.check(folder); err != nil {
		return 0, err
	}
	cmd := s.c.Append(folder, int64(len(raw)), &imap.AppendOptions{Flags: []imap.Flag{imap.FlagDraft, imap.FlagSeen}, Time: time.Now()})
	if _, err := cmd.Write(raw); err != nil {
		_ = cmd.Close()
		return 0, mapErr(ctx, err, ErrLost)
	}
	if err := cmd.Close(); err != nil {
		return 0, mapErr(ctx, err, ErrLost)
	}
	d, err := cmd.Wait()
	if err != nil {
		return 0, mapErr(ctx, err, ErrServer)
	}
	return uint32(d.UID), nil
}

// FindByMessageID returns the UID of a message of the folder with the
// Message-ID, or zero. It is how a retried draft finds an earlier attempt.
func (s *Session) FindByMessageID(ctx context.Context, folder, id string) (uint32, error) {
	if err := s.selectRO(ctx, folder); err != nil {
		return 0, err
	}
	data, err := s.c.UIDSearch(&imap.SearchCriteria{Header: []imap.SearchCriteriaHeaderField{{Key: "Message-ID", Value: strings.Trim(id, "<>")}}}, nil).Wait()
	if err != nil {
		return 0, mapErr(ctx, err, ErrServer)
	}
	if u := data.AllUIDs(); len(u) > 0 {
		return uint32(u[len(u)-1]), nil
	}
	return 0, nil
}
