// SPDX-License-Identifier: MIT
package hostsettings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mudler/LocalAI/pkg/egresspolicy"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestAddonSettingsAuthorityStore(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Addon settings authority store")
}
func ptr[T any](v T) *T { return &v }
func legacy() *Migration {
	return &Migration{Mode: Standalone, Source: "effective-owner", Version: "legacy-v1", Candidates: []Policy{{Ceiling: "registries", Default: "off", Ports: "443", ToolProviders: map[string]string{"bash": "sandbox"}}}}
}

var _ = Describe("Owner policy", func() {
	var dir, path string
	var s *Store
	ctx := context.Background()
	BeforeEach(func() {
		var err error
		dir, err = os.MkdirTemp("", "authority-")
		Expect(err).NotTo(HaveOccurred())
		path = filepath.Join(dir, "policy.json")
	})
	AfterEach(func() {
		if s != nil {
			Expect(s.Close()).To(Succeed())
			s = nil
		}
		Expect(os.RemoveAll(dir)).To(Succeed())
	})
	open := func() { var err error; s, err = Open(path, legacy()); Expect(err).NotTo(HaveOccurred()) }
	It("preserves effective migration, restarts, and reconciles durable receipts", func() {
		open()
		a, err := s.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Revision).To(Equal(uint64(1)))
		Expect(a.Policy).To(Equal(legacy().Candidates[0]))
		Expect(a.Migration.Source).To(Equal("effective-owner"))
		p := Patch{AllowedHosts: ptr("example.com")}
		b, err := s.Update(ctx, p, 1, "op-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(b.Revision).To(Equal(uint64(2)))
		_, err = s.Update(ctx, Patch{Ports: ptr("8443")}, 2, "op-b")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())
		s = nil
		open()
		replay, err := s.Update(ctx, p, 1, "op-a")
		Expect(err).NotTo(HaveOccurred())
		Expect(replay).To(Equal(b))
		_, err = s.Update(ctx, Patch{}, 1, "op-a")
		Expect(err).To(MatchError(ErrOperationMismatch))
		live, err := s.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(live.Revision).To(Equal(uint64(3)))
		Expect(live.Policy.Ports).To(Equal("8443"))
		live.Policy.ToolProviders["bash"] = "web"
		again, _ := s.Snapshot(ctx)
		Expect(again.Policy.ToolProviders["bash"]).To(Equal("sandbox"))
		info, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0600)))
	})
	It("turns private egress on only with the typed phrase, and never stores it", func() {
		open()
		_, err := s.Update(ctx, Patch{AllowPrivate: ptr(true)}, 1, "op-no-phrase")
		Expect(err).To(MatchError(ErrInvalid))
		_, err = s.Update(ctx, Patch{AllowPrivate: ptr(true), EgressPrivateConfirm: ptr("yes")}, 1, "op-wrong-phrase")
		Expect(err).To(MatchError(ErrInvalid))
		live, err := s.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(live.Revision).To(Equal(uint64(1)))
		Expect(live.Policy.AllowPrivate).To(BeFalse())

		got, err := s.Update(ctx, Patch{AllowPrivate: ptr(true), EgressPrivateConfirm: ptr(" " + egresspolicy.EgressPrivatePhrase + " ")}, 1, "op-phrase")
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Policy.AllowPrivate).To(BeTrue())
		raw, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(raw)).NotTo(ContainSubstring(egresspolicy.EgressPrivatePhrase))

		// Already on: later patches need no phrase, and switching off never does.
		_, err = s.Update(ctx, Patch{Ports: ptr("8443")}, 2, "op-ports")
		Expect(err).NotTo(HaveOccurred())
		off, err := s.Update(ctx, Patch{AllowPrivate: ptr(false)}, 3, "op-off")
		Expect(err).NotTo(HaveOccurred())
		Expect(off.Policy.AllowPrivate).To(BeFalse())
	})
	It("prunes oldest receipts and preserves restart replay without limiting updates", func() {
		open()
		// Seed the full retention window through real reducer transitions, then
		// persist it once to avoid quadratic disk writes in this boundary test.
		var st state = s.state
		patch := Patch{Ports: ptr("8443")}
		for i := 0; i < MaxReceipts; i++ {
			r := reduce(st, update{Patch: patch, Revision: uint64(i + 1), ID: fmt.Sprintf("op-%d", i)})
			Expect(r.Err).NotTo(HaveOccurred())
			st = reduce(r.State, persisted{}).State
		}
		s.state = st
		_, err := s.persist(st.(ready).doc)
		Expect(err).NotTo(HaveOccurred())
		last, err := s.Update(ctx, Patch{AllowedHosts: ptr("example.com")}, uint64(MaxReceipts+1), "newest")
		Expect(err).NotTo(HaveOccurred())
		Expect(last.Revision).To(Equal(uint64(MaxReceipts + 2)))
		Expect(s.state.(ready).doc.Receipts).To(HaveLen(MaxReceipts))
		Expect(s.state.(ready).doc.Receipts).NotTo(HaveKey("op-0"))
		Expect(s.Close()).To(Succeed())
		s = nil
		open()
		before, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		replay, err := s.Update(ctx, patch, 2, "op-1")
		Expect(err).NotTo(HaveOccurred())
		Expect(replay.Revision).To(Equal(uint64(3)))
		_, err = s.Update(ctx, Patch{}, 2, "op-1")
		Expect(err).To(MatchError(ErrOperationMismatch))
		_, err = s.Update(ctx, patch, 1, "op-0")
		Expect(err).To(MatchError(ErrConflict))
		live, err := s.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(live).To(Equal(last))
		after, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(after).To(Equal(before))
		_, err = s.Update(ctx, Patch{}, live.Revision, "continues")
		Expect(err).NotTo(HaveOccurred())
	})
	It("rejects trailing JSON in patches and durable documents", func() {
		var p Patch
		Expect(strictJSON([]byte(`{"chats_egress_ports":"443"} {}`), &p)).NotTo(Succeed())
		open()
		Expect(s.Close()).To(Succeed())
		s = nil
		original, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		for _, suffix := range []string{" {}", " null", " trailing"} {
			body := append(append([]byte{}, original...), []byte(suffix)...)
			Expect(os.WriteFile(path, body, 0600)).To(Succeed())
			other, err := Open(path, legacy())
			Expect(err).To(HaveOccurred())
			Expect(other).To(BeNil())
			after, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred())
			Expect(after).To(Equal(body))
		}
	})
	It("reopens canonical migration without provider pins", func() {
		m := legacy()
		m.Candidates[0].ToolProviders = nil
		var err error
		s, err = Open(path, m)
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())
		s = nil
		s, err = Open(path, nil)
		Expect(err).NotTo(HaveOccurred())
		live, err := s.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(live.Policy.ToolProviders).To(BeEmpty())
	})
	for name, body := range map[string]string{
		"duplicate ceiling":  `{"chats_egress_ceiling":"off","chats_egress_ceiling":"all"}`,
		"escaped duplicate":  `{"chats_egress_ceiling":"off","chats_egress_\u0063eiling":"all"}`,
		"duplicate provider": `{"chats_tool_providers":{"bash":null,"bash":"sandbox"}}`,
		"noncanonical field": `{"CHATS_EGRESS_CEILING":"all"}`,
	} {
		It("rejects patch "+name, func() {
			var p Patch
			Expect(json.Unmarshal([]byte(body), &p)).NotTo(Succeed())
		})
	}
	It("validates deletion keys before persistence even when absent", func() {
		open()
		before, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		calls := 0
		s.persist = func(document) (bool, error) { calls++; return false, nil }
		for _, key := range []string{"unknown", "", "BASH", "localai-workspace"} {
			_, err = s.Update(ctx, Patch{ToolProviders: map[string]*string{key: nil}}, 1, "delete-"+key)
			Expect(errors.Is(err, ErrInvalid)).To(BeTrue(), key)
		}
		Expect(calls).To(BeZero())
		live, err := s.Snapshot(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(live.Revision).To(Equal(uint64(1)))
		after, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(after).To(Equal(before))
	})
	It("rejects missing null and mistyped required document fields recursively", func() {
		open()
		_, err := s.Update(ctx, Patch{}, 1, "Mixed-Case-Receipt")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())
		s = nil
		original, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		var root map[string]any
		Expect(json.Unmarshal(original, &root)).To(Succeed())
		// Include both the current and receipt snapshots and all nested objects.
		var visit func(map[string]any, []string)
		visit = func(obj map[string]any, pathParts []string) {
			for key, value := range obj {
				fieldPath := append(append([]string{}, pathParts...), key)
				for _, replacement := range []string{"missing", "null", "wrong type"} {
					// A document written before the key existed has none, and
					// reads as the default (see files_ceiling_test.go).
					if replacement == "missing" && (key == "connector_files_ceiling_bytes" || key == "sandbox_profiles" || slices.Contains(pathParts, "sandbox_profiles")) {
						continue
					}
					var copyRoot map[string]any
					Expect(json.Unmarshal(original, &copyRoot)).To(Succeed())
					target := copyRoot
					for _, part := range pathParts {
						target = target[part].(map[string]any)
					}
					// An empty list is a list, so it is not a wrong type for one.
					if replacement == "wrong type" && (key == "pin_previous" || key == "roles" || key == "users") {
						continue
					}
					switch replacement {
					case "missing":
						delete(target, key)
					case "null":
						target[key] = nil
					default:
						target[key] = []any{}
					}
					body, err := json.Marshal(copyRoot)
					Expect(err).NotTo(HaveOccurred())
					Expect(os.WriteFile(path, body, 0600)).To(Succeed())
					other, openErr := Open(path, legacy())
					if other != nil {
						Expect(other.Close()).To(Succeed())
					}
					Expect(openErr).To(HaveOccurred(), "%v %s", fieldPath, replacement)
					after, err := os.ReadFile(path)
					Expect(err).NotTo(HaveOccurred())
					Expect(after).To(Equal(body))
				}
				if child, ok := value.(map[string]any); ok {
					// Map entries are optional; their values still have required fields.
					if key == "receipts" {
						for id, receipt := range child {
							visit(receipt.(map[string]any), append(fieldPath, id))
						}
					} else if key != "chats_tool_providers" {
						visit(child, fieldPath)
					}
				}
			}
		}
		visit(root, nil)
	})
	It("rejects duplicate and noncanonical document fields at every structure", func() {
		open()
		_, err := s.Update(ctx, Patch{}, 1, "Mixed-Case-Receipt")
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Close()).To(Succeed())
		s = nil
		original, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		for _, field := range []string{"snapshot", "receipts", "schema_version", "policy", "migration", "mode", "body", "chats_egress_ceiling", "bash"} {
			token := `"` + field + `":`
			// An extra null occurrence must not be silently overwritten.
			body := strings.Replace(string(original), token, token+`null,`+token, 1)
			Expect(os.WriteFile(path, []byte(body), 0600)).To(Succeed())
			other, err := Open(path, legacy())
			if other != nil {
				Expect(other.Close()).To(Succeed())
			}
			Expect(err).To(HaveOccurred(), field)
			if field == "bash" {
				continue
			} // Map keys are not struct field names.
			body = strings.Replace(string(original), token, `"`+strings.ToUpper(field)+`":`, 1)
			Expect(os.WriteFile(path, []byte(body), 0600)).To(Succeed())
			other, err = Open(path, legacy())
			if other != nil {
				Expect(other.Close()).To(Succeed())
			}
			Expect(err).To(HaveOccurred(), field)
		}
		// The canonical file accepts arbitrary valid operation IDs unchanged.
		Expect(os.WriteFile(path, original, 0600)).To(Succeed())
		open()
		replay, err := s.Update(ctx, Patch{}, 1, "Mixed-Case-Receipt")
		Expect(err).NotTo(HaveOccurred())
		Expect(replay.Revision).To(Equal(uint64(2)))
	})
	It("serializes concurrent stale writers and preserves disjoint reconciled patches", func() {
		open()
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for i, p := range []Patch{{AllowedHosts: ptr("example.com")}, {AutoUpgradeAddons: ptr(true)}} {
			wg.Add(1)
			go func(i int, p Patch) {
				defer wg.Done()
				_, err := s.Update(ctx, p, 1, []string{"a", "b"}[i])
				errs <- err
			}(i, p)
		}
		wg.Wait()
		close(errs)
		ok, conflict := 0, 0
		for err := range errs {
			if err == nil {
				ok++
			} else if errors.Is(err, ErrConflict) {
				conflict++
			}
		}
		Expect(ok).To(Equal(1))
		Expect(conflict).To(Equal(1))
		a, _ := s.Snapshot(ctx)
		_, err := s.Update(ctx, Patch{Ports: ptr("8443"), ToolProviders: map[string]*string{"web_fetch": ptr("web")}}, a.Revision, "reconcile")
		Expect(err).NotTo(HaveOccurred())
		a, _ = s.Snapshot(ctx)
		Expect(a.Policy.ToolProviders).To(HaveKeyWithValue("bash", "sandbox"))
		Expect(a.Policy.ToolProviders).To(HaveKeyWithValue("web_fetch", "web"))
	})
	It("supports explicit false, empty strings and per-key removal", func() {
		open()
		_, err := s.Update(ctx, Patch{AutoUpgradeAddons: ptr(true), AllowedHosts: ptr("example.com")}, 1, "enable")
		Expect(err).NotTo(HaveOccurred())
		a, err := s.Update(ctx, Patch{AutoUpgradeAddons: ptr(false), AllowedHosts: ptr(""), ToolProviders: map[string]*string{"bash": nil}}, 2, "clear")
		Expect(err).NotTo(HaveOccurred())
		Expect(a.Policy.AutoUpgradeAddons).To(BeFalse())
		Expect(a.Policy.AllowedHosts).To(BeEmpty())
		Expect(a.Policy.ToolProviders).NotTo(HaveKey("bash"))
		Expect(a.Policy.Ports).To(Equal("443"))
	})
	It("rejects malformed patches, unknown fields and invalid values without revision changes", func() {
		open()
		for _, body := range []string{`{"audit_days":3}`, `{"chats_egress_allow_private":null}`, `{"chats_tool_providers":null}`, `{"chats_egress_ports":7}`} {
			var p Patch
			Expect(json.Unmarshal([]byte(body), &p)).NotTo(Succeed())
		}
		for _, key := range []string{"localai-workspace", "office-quickstart", "unknown"} {
			Expect(validate(Policy{ToolProviders: map[string]string{key: "office"}})).To(MatchError(ContainSubstring(ErrInvalid.Error())))
		}
		for _, key := range []string{"bash", "web_search", "web_fetch", "xlsx", "docx", "pptx", "pdf"} {
			Expect(validate(Policy{ToolProviders: map[string]string{key: "office"}})).To(Succeed())
		}
		for i, p := range []Patch{{Ceiling: ptr("bogus")}, {Ports: ptr("22")}, {AllowedHosts: ptr("https://example.com")}, {ToolProviders: map[string]*string{"bash": ptr("../invalid")}}} {
			_, err := s.Update(ctx, p, 1, []string{"i1", "i2", "i3", "i4"}[i])
			Expect(err).To(HaveOccurred())
		}
		a, _ := s.Snapshot(ctx)
		Expect(a.Revision).To(Equal(uint64(1)))
	})
	It("does not publish or acknowledge failed persistence and fails closed after uncertain durability", func() {
		open()
		before, _ := os.ReadFile(path)
		s.persist = func(document) (bool, error) { return false, errors.New("disk full") }
		_, err := s.Update(ctx, Patch{Ceiling: ptr("all")}, 1, "failed")
		Expect(err).To(MatchError(ErrUnavailable))
		a, _ := s.Snapshot(ctx)
		Expect(a.Revision).To(Equal(uint64(1)))
		after, _ := os.ReadFile(path)
		Expect(after).To(Equal(before))
		s.persist = func(document) (bool, error) { return true, errors.New("directory sync failed") }
		_, err = s.Update(ctx, Patch{}, 1, "uncertain")
		Expect(err).To(MatchError(ErrUncertain))
		_, err = s.Snapshot(ctx)
		Expect(err).To(MatchError(ErrUnavailable))
	})
	It("rejects corruption, unsupported schema and a second writable owner", func() {
		open()
		other, err := Open(path, nil)
		Expect(err).To(HaveOccurred())
		Expect(other).To(BeNil())
		Expect(s.Close()).To(Succeed())
		s = nil
		for _, b := range []string{`{`, `{"snapshot":{"schema_version":99}}`, `{"unknown":true}`} {
			Expect(os.WriteFile(path, []byte(b), 0600)).To(Succeed())
			other, err = Open(path, legacy())
			Expect(err).To(HaveOccurred())
			Expect(other).To(BeNil())
			got, _ := os.ReadFile(path)
			Expect(string(got)).To(Equal(b))
		}
	})
	It("requires explicit owner migration in distributed mode and never combines candidates", func() {
		for _, m := range []*Migration{nil, {Mode: Distributed, Source: "frontend", Version: "v1", Candidates: legacy().Candidates}, {Mode: Standalone, Source: "ambiguous", Version: "v1", Candidates: append(legacy().Candidates, Policy{Ceiling: "all"})}} {
			a, err := Open(path, m)
			Expect(err).To(HaveOccurred())
			Expect(a).To(BeNil())
			_, err = os.Stat(path)
			Expect(os.IsNotExist(err)).To(BeTrue())
		}
		m := legacy()
		m.Mode = Distributed
		m.OwnerSelected = true
		var err error
		s, err = Open(path, m)
		Expect(err).NotTo(HaveOccurred())
		a, _ := s.Snapshot(ctx)
		Expect(a.Policy.Ceiling).To(Equal("registries"))
		Expect(a.Policy.Default).To(Equal("off"))
	})
	It("exercises reducer traces, persistence effects and bounded replay protection", func() {
		initial := ready{document{Snapshot: Snapshot{SchemaVersion: 1, Revision: 1}, Receipts: map[string]receipt{}}}
		traces := [][]event{{update{ID: "a", Revision: 1}, persisted{}}, {update{ID: "a", Revision: 1}, persisted{}, update{ID: "a", Revision: 1}}, {update{ID: "a", Revision: 1}, writeFailed{}}, {update{ID: "a", Revision: 1}, persisted{}, update{ID: "b", Revision: 1}}, {persisted{}, update{ID: "a", Revision: 1}, durabilityUnknown{}, update{ID: "b", Revision: 1}}}
		rules := [][]string{{"R1", "R6"}, {"R1", "R6", "R2"}, {"R1", "R7"}, {"R1", "R6", "R4"}, {"R0-ignored", "R1", "R8", "R9"}}
		for i, trace := range traces {
			var st state = initial
			got := []string{}
			for _, e := range trace {
				r := reduce(st, e)
				if r.Rule == "R1" {
					Expect(r.Persist).NotTo(BeNil())
					Expect(r.Reply.Revision).To(BeZero())
				}
				st = r.State
				got = append(got, r.Rule)
			}
			Expect(got).To(Equal(rules[i]))
			switch st.(type) {
			case ready, unavailable:
			default:
				Fail("trace stuck pending")
			}
		}

	})
})
