package studio

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/services/studio/hostmsg"
)

// Every sentence that the host writes to a person in a chat is a key of the
// host message catalog (hostmsg), spoken in the language of the person through
// msg or a Voice. These checks fail when a fixed text goes to the send path
// any other way, or when a key is not in the catalog.
//
// What is not covered on purpose, because the reader is the model and not the
// person: tool descriptions and tool results (talkbacktool.go, attachfile.go,
// the model branches of triggertools.go, changetools.go and
// connectorchange.go), the note of the held back tools (connectordenied.go),
// the thread block (connectorthread.go), the note of the chat
// (connectorturn.go), the reasons of the capability table (connectorreasons.go)
// and the log and audit lines. They stay English.
var _ = Describe("the sentences the host writes to a chat", func() {
	var (
		fset   *gotoken.FileSet
		parsed map[string]*ast.File
	)
	letters := regexp.MustCompile(`[A-Za-z]{2,}`)

	BeforeEach(func() {
		fset = gotoken.NewFileSet()
		parsed = map[string]*ast.File{}
		names, err := filepath.Glob("*.go")
		Expect(err).NotTo(HaveOccurred())
		for _, n := range names {
			if strings.HasSuffix(n, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, n, nil, 0)
			Expect(err).NotTo(HaveOccurred())
			parsed[n] = f
		}
		Expect(parsed).NotTo(BeEmpty())
	})

	// calleeName gives the last name of a called function: say, c.say, Post.
	calleeName := func(e ast.Expr) string {
		switch x := e.(type) {
		case *ast.Ident:
			return x.Name
		case *ast.SelectorExpr:
			return x.Sel.Name
		}
		return ""
	}

	// isKeyCall says whether a call speaks the catalog: v.T(key, name, value, ...),
	// tell(key, ...) or msg(ctx, binding, sender, key, ...). It returns the
	// values of the arguments, which are the only parts that may hold text.
	isKeyCall := func(x *ast.CallExpr) (values []ast.Expr, ok bool) {
		switch calleeName(x.Fun) {
		case "T", "tell":
			for i := 2; i < len(x.Args); i += 2 {
				values = append(values, x.Args[i])
			}
			return values, len(x.Args) > 0
		case "msg":
			for i := 5; i < len(x.Args); i += 2 {
				values = append(values, x.Args[i])
			}
			return values, len(x.Args) > 3
		}
		return nil, false
	}

	// fixedText reports the first fixed text in an expression that goes to a
	// chat: a string literal that has a word, or a name of the old sentence
	// constants.
	var fixedTextOf func(e ast.Expr) (string, gotoken.Pos)
	fixedText := func(e ast.Expr) (string, gotoken.Pos) { return fixedTextOf(e) }
	fixedTextOf = func(e ast.Expr) (string, gotoken.Pos) {
		var (
			found string
			at    gotoken.Pos
		)
		ast.Inspect(e, func(n ast.Node) bool {
			if found != "" {
				return false
			}
			switch x := n.(type) {
			case *ast.CallExpr:
				if values, ok := isKeyCall(x); ok {
					for _, v := range values {
						if s, p := fixedTextOf(v); s != "" && found == "" {
							found, at = s, p
						}
					}
					return false
				}
			case *ast.BasicLit:
				if x.Kind == gotoken.STRING {
					if s, _ := strconv.Unquote(x.Value); letters.MatchString(s) && !hostmsg.Has(s) {
						found, at = s, x.Pos()
					}
				}
			case *ast.Ident:
				if strings.HasPrefix(x.Name, "sent") && len(x.Name) > 4 && x.Name[4] >= 'A' && x.Name[4] <= 'Z' {
					found, at = x.Name, x.Pos()
				}
			}
			return found == ""
		})
		return found, at
	}

	// sinks are the places where text is written to a chat.
	sinks := func(f *ast.File, visit func(text ast.Expr, what string)) {
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				// say(text) of the handler, c.say(b, token, chat, thread, text)
				// and the say of triggers.
				if calleeName(x.Fun) == "say" && len(x.Args) > 0 {
					visit(x.Args[len(x.Args)-1], "say")
				}
			case *ast.CompositeLit:
				name := ""
				switch t := x.Type.(type) {
				case *ast.Ident:
					name = t.Name
				case *ast.SelectorExpr:
					name = t.Sel.Name
				}
				if name != "Post" && name != "Outgoing" {
					return true
				}
				for _, el := range x.Elts {
					if kv, ok := el.(*ast.KeyValueExpr); ok {
						if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "Text" {
							visit(kv.Value, name+".Text")
						}
					}
				}
			}
			return true
		})
	}

	It("reach the send path only as catalog keys", func() {
		count := 0
		for name, f := range parsed {
			sinks(f, func(text ast.Expr, what string) {
				count++
				if s, at := fixedText(text); s != "" {
					Fail(fset.Position(at).String() + " (" + name + "): " + strconv.Quote(s) + " goes to " + what +
						" as a fixed text; add a key to hostmsg/locales/*.json and write it with msg or a Voice")
				}
			})
		}
		Expect(count).To(BeNumerically(">", 5), "the guard found no send path; it is out of date")
	})

	It("use only keys that are in the catalog", func() {
		used := map[string]bool{}
		prefixes := map[string]bool{}
		for _, f := range parsed {
			ast.Inspect(f, func(n ast.Node) bool {
				if l, ok := n.(*ast.BasicLit); ok && l.Kind == gotoken.STRING {
					s, _ := strconv.Unquote(l.Value)
					used[s] = true
				}
				return true
			})
		}
		// The package of the catalog speaks some keys itself (Voice.Join).
		own, err := parser.ParseFile(fset, "hostmsg/catalog.go", nil, 0)
		Expect(err).NotTo(HaveOccurred())
		ast.Inspect(own, func(n ast.Node) bool {
			if l, ok := n.(*ast.BasicLit); ok && l.Kind == gotoken.STRING {
				s, _ := strconv.Unquote(l.Value)
				used[s] = true
			}
			return true
		})
		for name, f := range parsed {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				fn := calleeName(call.Fun)
				key := call.Args[0]
				if fn == "msg" && len(call.Args) > 3 {
					key = call.Args[3] // msg(ctx, binding, sender, key, ...)
				} else if fn != "T" && fn != "tell" {
					return true
				}
				switch k := key.(type) {
				case *ast.BasicLit:
					s, _ := strconv.Unquote(k.Value)
					Expect(hostmsg.Has(s)).To(BeTrue(), "%s (%s): %q is not a key of the host message catalog", fset.Position(k.Pos()), name, s)
				case *ast.BinaryExpr:
					if l, ok := k.X.(*ast.BasicLit); ok {
						s, _ := strconv.Unquote(l.Value)
						prefixes[s] = true
					}
				}
				return true
			})
		}
		// A key that nothing uses is a sentence nobody can reach: drop it, so
		// the catalog does not hold dead translations.
		for _, k := range hostmsg.Keys() {
			if used[k] {
				continue
			}
			byPrefix := false
			for p := range prefixes {
				byPrefix = byPrefix || strings.HasPrefix(k, p)
			}
			Expect(byPrefix).To(BeTrue(), "the key %q is in the catalog but no code uses it", k)
		}
	})

	// producers build the sentences of the host. They may hold no fixed
	// English text except in a log, an audit line or an error for the log.
	producers := []string{
		"newChat", "tryPairing", "answerPhrase", "answerChange", "undoChange", "answerChanges", "proposalChatText",
		"sendFiles", "sendHandled", "media", "transcribe", "sentFileTooBig", "noteSendTooBig", "answerOf", "inputs",
		"AnswerByPhrase", "answerBare", "answerProposal", "cancelAuto", "proposalText", "autoText", "costText", "whenTextIn",
		"summaryIn", "scheduleWords", "actsText", "actWords", "actLabel", "TextIn", "autoVerdict", "actsVerdict",
		"resultsGo", "gapTextIn", "formatFileSizeIn", "whyIn", "sentenceIn", "kindWords", "scopeWords", "view", "observe",
		"reportFailure", "sweepTimers",
	}
	quiet := []string{"Info", "Warn", "Error", "Debug", "audit", "New", "Errorf", "bad", "connectorBad", "Is", "Wrap"}

	It("hold no fixed English text where a person reads it", func() {
		seen := map[string]bool{}
		var bad []string
		for name, f := range parsed {
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Body == nil || !slices.Contains(producers, fd.Name.Name) {
					continue
				}
				seen[fd.Name.Name] = true
				var walk func(n ast.Node, inQuiet bool)
				walk = func(n ast.Node, inQuiet bool) {
					ast.Inspect(n, func(c ast.Node) bool {
						switch x := c.(type) {
						case *ast.CallExpr:
							if slices.Contains(quiet, calleeName(x.Fun)) {
								return false // a log line, an audit line or an error for the log
							}
							if values, ok := isKeyCall(x); ok {
								for _, v := range values {
									walk(v, inQuiet)
								}
								return false
							}
						case *ast.BasicLit:
							if x.Kind != gotoken.STRING {
								return true
							}
							s, _ := strconv.Unquote(x.Value)
							if wordy.MatchString(s) && !hostmsg.Has(s) {
								bad = append(bad, fset.Position(x.Pos()).String()+" ("+name+"."+fd.Name.Name+"): "+strconv.Quote(s))
							}
						}
						return true
					})
				}
				walk(fd.Body, false)
			}
		}
		Expect(bad).To(BeEmpty(), "these are fixed texts in functions that write to a person; use a catalog key")
		for _, p := range producers {
			Expect(seen).To(HaveKey(p), "the guard lists %s but no such function exists; update the list", p)
		}
	})
})

// wordy matches a text with two words, which is a sentence and not a key, a
// format or a separator.
var wordy = regexp.MustCompile(`[A-Za-z]{2,}[ ,.:;]+[A-Za-z]{2,}`)
