package studio

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/mudler/LocalAI/core/services/studio/connectors"
	"github.com/mudler/LocalAI/core/services/studio/hostmsg"
	"github.com/mudler/LocalAI/pkg/addons/contract"
)

// FileLayer names the layer that sets a file limit. The limit in effect is the
// smallest of the layers, and the sentences to people name the one that won.
type FileLayer string

const (
	// LayerOwner is the limit that the owner set for one binding.
	LayerOwner FileLayer = "owner"
	// LayerAddon is the ability that the connector kind declares.
	LayerAddon FileLayer = "addon"
	// LayerHost is the host ceiling of the owner policy.
	LayerHost FileLayer = "host"
)

// FileLimit is the largest file that may move in one direction, and the
// layer that sets it.
type FileLimit struct {
	Bytes int64
	Layer FileLayer
}

// EffectiveFileLimit is the one place that decides how large a file may be.
// It takes the smallest of the host ceiling (zero: the default), the ability
// the kind declares for the direction (nil: none) and the owner's limit for the
// binding (zero: none). On a tie the layer that a person can change first is
// named: the owner, the add-on, then the host.
func EffectiveFileLimit(ceiling int64, declared *contract.FileAbility, owner int64) FileLimit {
	if ceiling <= 0 {
		ceiling = contract.DefaultFileCeilingBytes
	}
	// The host ceiling is always set, so it is the first layer.
	limit := FileLimit{Bytes: math.MaxInt64, Layer: LayerHost}
	for _, c := range []FileLimit{{ceiling, LayerHost}, abilityLimit(declared), {owner, LayerOwner}} {
		if c.Bytes > 0 && c.Bytes <= limit.Bytes {
			limit = c
		}
	}
	return limit
}

func abilityLimit(a *contract.FileAbility) FileLimit {
	if a == nil {
		return FileLimit{}
	}
	return FileLimit{Bytes: a.MaxBytes, Layer: LayerAddon}
}

// FileLimits are the limits of one binding.
type FileLimits struct {
	Receive, Send FileLimit
	// ReceiveMax and SendMax are the most an owner may set: the ability of
	// the kind under the host ceiling. Zero: the kind moves no files.
	ReceiveMax, SendMax int64
	// ReceiveDeclared and SendDeclared are the abilities the kind declares;
	// zero when it declares none.
	ReceiveDeclared, SendDeclared int64
	// HostCeiling is the host ceiling in effect, in bytes.
	HostCeiling int64
	// ReceiveTypes and SendTypes are the media types the kind declares for a
	// direction; empty means every type the host allows.
	ReceiveTypes, SendTypes []string
	// KindTitle is the name of the platform, for the sentences.
	KindTitle string
}

// fileCeiling reads the host ceiling of the owner policy. A failure reads as
// the default: the layers below still bind, and the ceiling never lifts them.
func (c *Connectors) fileCeiling(ctx context.Context) int64 {
	if c.o.FileCeiling == nil {
		return contract.DefaultFileCeilingBytes
	}
	n, err := c.o.FileCeiling(ctx)
	if err != nil || n <= 0 {
		if err != nil {
			c.log.Warn("studio: the host ceiling of connector files is not readable", "error", err)
		}
		return contract.DefaultFileCeilingBytes
	}
	return n
}

// FileLimits resolves the limits of a binding now. The kind is read from the
// add-ons that run; a binding whose add-on does not run has declared nothing.
func (c *Connectors) FileLimits(ctx context.Context, b connectors.Binding) FileLimits {
	var files *contract.ConnectorFiles
	title := ""
	if kinds, err := c.o.Transport.Kinds(ctx); err == nil {
		if k, ok := kindFor(kinds, b); ok {
			files, title = k.Files, k.Title
		}
	}
	return resolveFileLimits(c.fileCeiling(ctx), files, title, b)
}

// KindFileLimits resolves the limits of a kind for a binding that has no
// limit of its own, such as one that is not made yet.
func (c *Connectors) KindFileLimits(ctx context.Context, k ConnectorKind) FileLimits {
	return resolveFileLimits(c.fileCeiling(ctx), k.Files, k.Title, connectors.Binding{})
}

func resolveFileLimits(ceiling int64, files *contract.ConnectorFiles, title string, b connectors.Binding) FileLimits {
	if files == nil {
		// A kind that declares no files ability moves no file: every limit is
		// zero.
		return FileLimits{KindTitle: title, HostCeiling: cmp.Or(ceiling, contract.DefaultFileCeilingBytes),
			Receive: FileLimit{Layer: LayerAddon}, Send: FileLimit{Layer: LayerAddon}}
	}
	l := FileLimits{KindTitle: title, HostCeiling: cmp.Or(ceiling, contract.DefaultFileCeilingBytes)}
	l.Receive = EffectiveFileLimit(ceiling, files.Receive, b.FileReceiveMax)
	l.Send = EffectiveFileLimit(ceiling, files.Send, b.FileSendMax)
	l.ReceiveMax = ownerMax(ceiling, files.Receive)
	l.SendMax = ownerMax(ceiling, files.Send)
	if files.Receive != nil {
		l.ReceiveDeclared, l.ReceiveTypes = files.Receive.MaxBytes, files.Receive.Types
	}
	if files.Send != nil {
		l.SendDeclared, l.SendTypes = files.Send.MaxBytes, files.Send.Types
	}
	return l
}

// ownerMax is the largest value an owner may set: what the kind declares for
// the direction (the host ceiling, if it declares nothing), under the host
// ceiling.
func ownerMax(ceiling int64, a *contract.FileAbility) int64 {
	if ceiling <= 0 {
		ceiling = contract.DefaultFileCeilingBytes
	}
	if a == nil {
		return ceiling
	}
	return min(a.MaxBytes, ceiling)
}

// checkOwnerLimits refuses a limit above what the kind allows: an owner can
// only lower it. A kind that does not run has no limit to compare with.
func (c *Connectors) checkOwnerLimits(ctx context.Context, b connectors.Binding, in ConnectorInput) error {
	// Only a value that this request writes is checked: a stored value that
	// the kind has lowered since must not block an unrelated update.
	if (in.FileReceiveMax == nil || *in.FileReceiveMax == 0) && (in.FileSendMax == nil || *in.FileSendMax == 0) {
		return nil
	}
	kinds, err := c.o.Transport.Kinds(ctx)
	if err != nil {
		return nil
	}
	k, ok := kindFor(kinds, b)
	if !ok {
		return nil
	}
	l := resolveFileLimits(c.fileCeiling(ctx), k.Files, k.Title, b)
	for _, f := range []struct {
		what         string
		set, allowed int64
	}{{"receive", b.FileReceiveMax, l.ReceiveMax}, {"send", b.FileSendMax, l.SendMax}} {
		if f.set > f.allowed {
			return connectorBad("the %s file limit can only lower the limit of the kind: at most %s", f.what, formatFileSize(f.allowed))
		}
	}
	return nil
}

// why names the layer that sets the limit, in words a person knows.
func (l FileLimit) why(kindTitle string) string { return l.whyIn(hostmsg.Voice{}, kindTitle) }

// whyIn is why in the language of the voice.
func (l FileLimit) whyIn(v hostmsg.Voice, kindTitle string) string {
	switch l.Layer {
	case LayerOwner:
		return v.T("file.why_owner")
	case LayerAddon:
		if kindTitle == "" {
			kindTitle = v.T("file.why_addon_default")
		}
		return v.T("file.why_addon", "kind", kindTitle)
	default:
		return v.T("file.why_platform")
	}
}

// sentence is the limit with the layer that sets it, such as
// "1 MiB (limit of this add-on version)".
func (l FileLimit) sentence(kindTitle string) string { return l.sentenceIn(hostmsg.Voice{}, kindTitle) }

// sentenceIn is sentence in the language of the voice.
func (l FileLimit) sentenceIn(v hostmsg.Voice, kindTitle string) string {
	return v.T("file.limit", "size", formatFileSizeIn(v, l.Bytes), "why", l.whyIn(v, kindTitle))
}

// formatFileSize writes a size in bytes, KiB, MiB, GiB or TiB, with at most
// one decimal.
func formatFileSize(n int64) string { return formatFileSizeIn(hostmsg.Voice{}, n) }

// formatFileSizeIn writes a size for a person who reads the language of the
// voice: the word for bytes and the decimal mark of the language.
func formatFileSizeIn(v hostmsg.Voice, n int64) string {
	if n == 1 {
		return v.T("size.byte")
	}
	if n < 1<<10 {
		return v.T("size.bytes", "n", strconv.FormatInt(n, 10))
	}
	units := []string{"KiB", "MiB", "GiB", "TiB"}
	val := float64(n) / 1024
	i := 0
	for val >= 1024 && i < len(units)-1 {
		val /= 1024
		i++
	}
	out := strings.TrimSuffix(strconv.FormatFloat(val, 'f', 1, 64), ".0") + " " + units[i]
	if v.Lang != "" && v.Lang != hostmsg.English {
		out = strings.Replace(out, ".", ",", 1)
	}
	return out
}

// Sentences to people. Each is built from the limit in effect.

// sentFileTooBig answers a person whose file the bot cannot read. size is
// zero when the add-on did not say how large the file is.
func (c *Connectors) sentFileTooBig(v hostmsg.Voice, l FileLimits, size int64) string {
	seen := ""
	if size > 0 {
		seen = " (" + formatFileSizeIn(v, size) + ")"
	}
	return v.T("file.too_big", "seen", seen, "max", formatFileSizeIn(v, l.Receive.Bytes), "why", l.Receive.whyIn(v, l.KindTitle))
}

// noteSendTooBig tells a person that a file the agent made stays in the chat.
func noteSendTooBig(v hostmsg.Voice, name string, l FileLimits) string {
	return v.T("file.send_too_big", "name", name, "limit", l.Send.sentenceIn(v, l.KindTitle))
}

// refuseAttachTooBig is the text that the attach tool gives the model.
func refuseAttachTooBig(name string, size int64, l FileLimits) error {
	return fmt.Errorf("%s is too large to send here (%s; the limit is %s). Tell the person; offer to split it, to make a smaller file, or to paste the content",
		name, formatFileSize(size), l.Send.sentence(l.KindTitle))
}
