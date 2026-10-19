// SPDX-License-Identifier: MIT
package migration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestRuntimeMigration(t *testing.T) {
	RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "Runtime migration")
}

type memorySink struct {
	values map[string][]byte
	fail   bool
}

func (s *memorySink) Put(_ context.Context, key string, data []byte) error {
	if s.fail {
		return errors.New("SECRET sink failure")
	}
	if s.values == nil {
		s.values = map[string][]byte{}
	}
	if old, ok := s.values[key]; ok && !bytes.Equal(old, data) {
		return errors.New("SECRET conflict")
	}
	s.values[key] = bytes.Clone(data)
	return nil
}
func source(format SourceFormat, data string) Source {
	return Source{Format: format, Data: []byte(data), Principal: "alice"}
}
func analyze(s Source) (*Plan, Report) {
	p, r, e := Analyze(s)
	Expect(e).NotTo(HaveOccurred())
	return p, r
}
func backed(p *Plan) PrivateBundle {
	_, e := Backup(context.Background(), p, &memorySink{})
	Expect(e).NotTo(HaveOccurred())
	b, e := Convert(p)
	Expect(e).NotTo(HaveOccurred())
	return b
}
func encoded(b PrivateBundle) []byte {
	data, e := EncodePrivateBundle(b)
	Expect(e).NotTo(HaveOccurred())
	return data
}
func invalid(s Source) {
	p, r, e := Analyze(s)
	if e == nil {
		Expect(r.Invalid).To(BeNumerically(">", 0))
		_, e = Backup(context.Background(), p, &memorySink{})
		Expect(e).NotTo(HaveOccurred())
		_, e = Convert(p)
	}
	Expect(e).To(HaveOccurred())
	Expect(e.Error()).NotTo(ContainSubstring("SECRET"))
}

var _ = ginkgo.Describe("Offline migration", func() {
	ginkgo.It("requires backup before even a minimal conversion", func() {
		p, _ := analyze(source(LocalAIDefinition, `{"name":"keeper"}`))
		_, e := Convert(p)
		Expect(e).To(HaveOccurred())
		b := backed(p)
		Expect(b.wire.Definitions[0].Executable).To(BeFalse())
	})
	for _, f := range []SourceFormat{LocalAIDefinition, LocalAGIDefinition} {
		f := f
		ginkgo.It("preserves and maps a single "+string(f), func() {
			s := source(f, ` {"name":"keeper","api_key":"SECRET","model":"m","description":"d","api_url":"https://SECRET","system_prompt":"s"} `)
			p, r := analyze(s)
			Expect(r.Mapped).To(Equal(6))
			b := backed(p)
			Expect(b.wire.Source.Data).To(Equal(s.Data))
			Expect(b.wire.Definitions[0].Normalized["api_key"]).To(Equal("SECRET"))
			Expect(b.wire.Definitions[0].Principal).To(Equal("alice"))
		})
	}
	ginkgo.It("preserves root and two-principal pool identities without grants", func() {
		p, _ := analyze(source(LocalAGIPool, `{"alice:keeper":{"name":"alice:keeper"},"bob:keeper":{"name":"bob:keeper"},"anon":{"name":"anon"}}`))
		d := backed(p).wire.Definitions
		Expect(d).To(HaveLen(3))
		Expect(d[0].Principal).To(Equal("alice"))
		Expect(d[1].Principal).To(Equal(InstallationOwner))
		Expect(d[2].Principal).To(Equal("bob"))
		for _, v := range d {
			Expect(v.Executable).To(BeFalse())
		}
	})
	ginkgo.It("rejects pool key mismatches and malformed identities", func() {
		for _, data := range []string{`{"alice:keeper":{"name":"keeper"}}`, `{":x":{"name":":x"}}`, `{"a:b:c":{"name":"a:b:c"}}`, `{"":{"name":""}}`} {
			invalid(source(LocalAGIPool, data))
		}
	})
	ginkgo.It("rejects logical root identity collisions", func() {
		invalid(source(LocalAGIPool, fmt.Sprintf(`{"anon":{"name":"anon"},"%s:anon":{"name":"%s:anon"}}`, InstallationOwner, InstallationOwner)))
	})
	ginkgo.It("preserves record status and timestamps as metadata", func() {
		s := source(LocalAIRecordSnapshot, `{"user_id":"alice","name":"keeper","status":"paused","id":"SECRET","created_at":"2026-10-19T00:00:00Z","updated_at":"2026-10-19T00:00:00Z","last_run_at":null,"config":{"name":"keeper","model":"m"}}`)
		p, r := analyze(s)
		Expect(r.Unsupported).To(BeNumerically(">", 0))
		b := backed(p)
		Expect(string(b.wire.Source.Data)).To(Equal(string(s.Data)))
		Expect(b.wire.Definitions[0].Metadata["status"]).To(Equal(json.RawMessage(`"paused"`)))
		Expect(b.wire.Definitions[0].Executable).To(BeFalse())
	})
	ginkgo.It("rejects record owner mismatch and hidden-column exports", func() {
		invalid(source(LocalAIRecordSnapshot, `{"user_id":"bob","name":"k","config":{"name":"k"}}`))
		invalid(source(LocalAIRecordSnapshot, `{"user_id":"alice","name":"k"}`))
	})
	ginkgo.It("classifies both action spellings without granting tools", func() {
		for _, s := range []Source{source(LocalAIDefinition, `{"name":"k","actions":[{"type":"search","config":"SECRET"}]}`), source(LocalAGIDefinition, `{"name":"k","actions":[{"name":"search","config":"SECRET"}]}`)} {
			p, r := analyze(s)
			Expect(r.Invalid).To(BeZero())
			Expect(r.Unsupported).To(BeNumerically(">=", 3))
			Expect(backed(p).wire.Definitions[0].Unsupported).To(ContainElement("actions[0].config"))
		}
	})
	ginkgo.It("rejects action alias conflicts and wrong source spelling", func() {
		invalid(source(LocalAIDefinition, `{"name":"k","actions":[{"type":"search","name":"other"}]}`))
		invalid(source(LocalAGIDefinition, `{"name":"k","actions":[{"type":"search"}]}`))
	})
	ginkgo.It("accounts all integrations and arbitrary nested keys privately", func() {
		s := source(LocalAIDefinition, `{"name":"k","connectors":[{"type":"slack","config":"SECRET","SECRET":{"SECRET":false}}],"filters":[{"type":"f","config":"{}"}],"dynamic_prompts":[{"type":"p","config":"{}"}],"mcp_servers":[{"url":"https://SECRET","token":"SECRET"}]}`)
		p, r := analyze(s)
		Expect(r.Invalid).To(BeZero())
		Expect(r.Findings).To(ContainElement(HaveField("Path", "connectors[0].unknown[1].unknown[1]")))
		Expect(backed(p).wire.Definitions[0].Unsupported).To(ContainElement("connectors[0].SECRET.SECRET"))
	})
	ginkgo.It("validates integration field types rather than dropping entries", func() {
		for _, field := range []string{`"connectors":[7]`, `"filters":[{"type":false}]`, `"dynamic_prompts":{}`, `"mcp_servers":[{"url":3,"token":false}]`} {
			invalid(source(LocalAIDefinition, `{"name":"k",`+field+`}`))
		}
	})
	ginkgo.It("accepts native MCP stdio arrays without executable authority", func() {
		p, r := analyze(source(LocalAIDefinition, `{"name":"k","mcp_stdio_servers":[{"name":"srv","cmd":"SECRET","args":["x"],"env":["KEY=SECRET"]}]}`))
		Expect(r.Invalid).To(BeZero())
		Expect(backed(p).wire.Definitions[0].Unsupported).To(ContainElement("mcp_stdio_servers[0].cmd"))
	})
	ginkgo.It("accepts Claude MCP wrappers in both sources and LocalAI cmd alias", func() {
		for _, f := range []SourceFormat{LocalAIDefinition, LocalAGIDefinition} {
			p, r := analyze(source(f, `{"name":"k","mcp_stdio_servers":"{\"mcpServers\":{\"SECRET\":{\"command\":\"SECRET\",\"args\":[\"x\"],\"env\":{\"SECRET\":\"SECRET\"}}}}"}`))
			Expect(r.Invalid).To(BeZero())
			backed(p)
		}
		_, r := analyze(source(LocalAIDefinition, `{"name":"k","mcp_stdio_servers":"{\"mcpServers\":{\"s\":{\"cmd\":\"x\"}}}"}`))
		Expect(r.Invalid).To(BeZero())
	})
	ginkgo.It("rejects malformed ambiguous and duplicate encoded MCP", func() {
		for _, v := range []string{`"SECRET"`, `"{\"mcpServers\":{},\"mcpServers\":{}}"`, `"{\"mcpServers\":{\"s\":{\"command\":\"x\",\"cmd\":\"x\"}}}"`, `[{"cmd":"x","command":"x"}]`, `[{"cmd":"x","args":[1]}]`} {
			invalid(source(LocalAIDefinition, `{"name":"k","mcp_stdio_servers":`+v+`}`))
		}
	})
	for _, format := range []SourceFormat{LocalAIDefinition, LocalAGIDefinition} {
		for _, wrapper := range []bool{false, true} {
			ginkgo.It(fmt.Sprintf("accounts nested children of wrong-shaped encoded MCP in %s wrapper=%t", format, wrapper), func() {
				text := `[{"SECRET":{"SECRET":[false,null,0]}}]`
				prefix := "mcp_stdio_servers"
				if wrapper {
					text = `{"mcpServers":` + text + `}`
					prefix += ".mcpServers"
				}
				value, err := json.Marshal(text)
				Expect(err).NotTo(HaveOccurred())
				s := source(format, `{"name":"k","mcp_stdio_servers":`+string(value)+`}`)
				p, r := analyze(s)
				Expect(r.Invalid).To(Equal(1))
				Expect(r.Findings).To(ContainElement(And(HaveField("Path", prefix), HaveField("Disposition", "invalid"))))
				public := prefix + "[0].unknown[1].unknown[1]"
				exact := prefix + "[0].SECRET.SECRET"
				publicChildren := []string{prefix + "[0]", prefix + "[0].unknown[1]", public, public + "[0]", public + "[1]", public + "[2]"}
				exactPaths := []string{"mcp_stdio_servers"}
				if wrapper {
					exactPaths = append(exactPaths, prefix)
				}
				exactPaths = append(exactPaths, prefix+"[0]", prefix+"[0].SECRET", exact, exact+"[0]", exact+"[1]", exact+"[2]")
				var children []string
				for _, finding := range r.Findings {
					if strings.HasPrefix(finding.Path, prefix+"[") {
						Expect(finding.Disposition).To(Equal("unsupported"))
						children = append(children, finding.Path)
					}
				}
				Expect(children).To(Equal(publicChildren))
				Expect(p.wire.Definitions[0].Unsupported).To(ConsistOf(exactPaths))
				Expect(p.wire.Definitions[0].Presence["mcp_stdio_servers"]).To(Equal(json.RawMessage(value)))
				Expect(p.wire.Source.Data).To(Equal(s.Data))
				_, err = Backup(context.Background(), p, &memorySink{})
				Expect(err).NotTo(HaveOccurred())
				_, err = Convert(p)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).NotTo(ContainSubstring("SECRET"))
				for _, safe := range []any{p, *p, r} {
					data, err := json.Marshal(safe)
					Expect(err).NotTo(HaveOccurred())
					Expect(string(data)).NotTo(ContainSubstring("SECRET"))
					for _, verb := range []string{"%v", "%+v", "%#v"} {
						Expect(fmt.Sprintf(verb, safe)).NotTo(ContainSubstring("SECRET"))
					}
				}
			})
		}
	}
	ginkgo.It("validates tool arrays CSV newline and null without preapproval", func() {
		for _, v := range []string{`["search"," stop "]`, `"search,stop\nupdate_state"`, `null`} {
			p, r := analyze(source(LocalAIDefinition, `{"name":"k","allowed_tools":`+v+`,"excluded_tools":`+v+`}`))
			Expect(r.Invalid).To(BeZero())
			Expect(backed(p).wire.Definitions[0].Normalized).NotTo(HaveKey("allowed_tools"))
		}
		invalid(source(LocalAIDefinition, `{"name":"k","allowed_tools":[false]}`))
	})
	ginkgo.It("parses exact int64 numbers and format-specific numeric strings", func() {
		for _, v := range []string{`9223372036854775807`, `-9223372036854775808`, `0`} {
			_, r := analyze(source(LocalAIDefinition, `{"name":"k","parallel_jobs":`+v+`}`))
			Expect(r.Invalid).To(BeZero())
		}
		for _, field := range strings.Fields("max_evaluation_loops max_attempts required_tool_before_finish_attempts parallel_jobs kb_results") {
			p, r := analyze(source(LocalAGIDefinition, `{"name":"k","`+field+`":"2"}`))
			Expect(r.Invalid).To(BeZero())
			backed(p)
			invalid(source(LocalAIDefinition, `{"name":"k","`+field+`":"2"}`))
		}
	})
	ginkgo.It("rejects fractional overflowing exponent and malformed numeric values", func() {
		for _, v := range []string{`1.5`, `9223372036854775808`, `-9223372036854775809`, `1e100`, `"2.5"`, `"SECRET"`, `true`} {
			invalid(source(LocalAGIDefinition, `{"name":"k","parallel_jobs":`+v+`}`))
		}
		invalid(source(LocalAGIDefinition, `{"name":"k","loop_detection":"2"}`))
	})
	ginkgo.It("retains cancellation absent null false and true distinctly", func() {
		for _, v := range []string{"", `,"cancel_previous_on_new_message":null`, `,"cancel_previous_on_new_message":false`, `,"cancel_previous_on_new_message":true`} {
			p, r := analyze(source(LocalAIDefinition, `{"name":"k"`+v+`}`))
			Expect(r.Invalid).To(BeZero())
			d := backed(p).wire.Definitions[0]
			raw, ok := d.Presence["cancel_previous_on_new_message"]
			Expect(ok).To(Equal(v != ""))
			if ok {
				Expect(v).To(HaveSuffix(string(raw)))
			}
		}
	})
	ginkgo.It("rejects duplicate keys trailing data and malformed envelope JSON safely", func() {
		for _, s := range []string{`{"name":"SECRET","name":"x"}`, `{"name":"k"} {}`, `{"SECRET":`, `[]`, `null`, `{"name":"k","x":{"a":1,"a":2}}`} {
			_, _, e := Analyze(source(LocalAIDefinition, s))
			Expect(e).To(HaveOccurred())
			Expect(e.Error()).NotTo(ContainSubstring("SECRET"))
		}
	})
	ginkgo.It("never exposes sentinels through reports errors or default formatting", func() {
		p, r := analyze(source(LocalAIDefinition, `{"name":"SECRET","api_key":"SECRET","SECRET":{"SECRET":"SECRET"}}`))
		b := backed(p)
		for _, v := range []any{p, *p, b, &b, r} {
			data, e := json.Marshal(v)
			Expect(e).NotTo(HaveOccurred())
			Expect(string(data)).NotTo(ContainSubstring("SECRET"))
			for _, format := range []string{"%v", "%+v", "%#v"} {
				Expect(fmt.Sprintf(format, v)).NotTo(ContainSubstring("SECRET"))
			}
		}
		Expect(string(encoded(b))).To(ContainSubstring("SECRET"))
	})
	ginkgo.It("backs up exact private source bytes with idempotent create-only keys", func() {
		s := source(LocalAIDefinition, " {\"name\":\"k\"} \n")
		s.Sidecars = map[string][]byte{"state": []byte("SECRET")}
		p, _ := analyze(s)
		sink := &memorySink{}
		r, e := Backup(context.Background(), p, sink)
		Expect(e).NotTo(HaveOccurred())
		r2, e := Backup(context.Background(), p, sink)
		Expect(e).NotTo(HaveOccurred())
		Expect(r2).To(Equal(r))
		Expect(sink.values).To(HaveLen(1))
		for _, v := range sink.values {
			var a backupArchive
			Expect(json.Unmarshal(v, &a)).To(Succeed())
			Expect(a.Source.Data).To(Equal(s.Data))
			Expect(a.Source.Sidecars).To(Equal(s.Sidecars))
		}
	})
	ginkgo.It("failed backup and sink conflicts cannot unlock any other plan", func() {
		p, _ := analyze(source(LocalAIDefinition, `{"name":"k"}`))
		_, e := Backup(context.Background(), p, &memorySink{fail: true})
		Expect(e.Error()).NotTo(ContainSubstring("SECRET"))
		_, e = Convert(p)
		Expect(e).To(HaveOccurred())
		sink := &memorySink{}
		_, e = Backup(context.Background(), p, sink)
		Expect(e).NotTo(HaveOccurred())
		q, _ := analyze(source(LocalAIDefinition, `{"name":"k"}`))
		_, e = Convert(q)
		Expect(e).To(HaveOccurred())
		for k := range sink.values {
			sink.values[k] = []byte("conflict")
		}
		_, e = Backup(context.Background(), q, sink)
		Expect(e).To(HaveOccurred())
		_, e = Convert(q)
		Expect(e).To(HaveOccurred())
	})
	ginkgo.It("owns inputs reports bundles and returned encodings independently", func() {
		s := source(LocalAIDefinition, `{"name":"k","api_key":"SECRET"}`)
		s.Sidecars = map[string][]byte{"history": []byte("SECRET")}
		p, r := analyze(s)
		s.Data[0] = '!'
		s.Sidecars["history"][0] = '!'
		r.Findings[0].Path = "SECRET"
		b := backed(p)
		original := encoded(b)
		b.wire.Source.Data[0] = '!'
		b2, e := Convert(p)
		Expect(e).NotTo(HaveOccurred())
		Expect(encoded(b2)).To(Equal(original))
		data := encoded(b2)
		data[0] = '!'
		Expect(encoded(b2)).To(Equal(original))
	})
	ginkgo.It("produces deterministic versioned validated private bundles", func() {
		s := source(LocalAIDefinition, `{"name":"k","unknown":0}`)
		p, _ := analyze(s)
		first := encoded(backed(p))
		q, _ := analyze(s)
		Expect(encoded(backed(q))).To(Equal(first))
		b, e := DecodePrivateBundle(first)
		Expect(e).NotTo(HaveOccurred())
		Expect(encoded(b)).To(Equal(first))
	})
	ginkgo.It("rejects tampered marker digest payload and execution flags", func() {
		p, _ := analyze(source(LocalAIDefinition, `{"name":"k"}`))
		data := encoded(backed(p))
		for _, pair := range [][2]string{{Version, "localai-agent-migration/v9"}, {`"executable":false`, `"executable":true`}, {`"digest":"`, `"digest":"bad`}, {`"principal":"alice"`, `"principal":"bob"`}} {
			mut := bytes.Replace(data, []byte(pair[0]), []byte(pair[1]), 1)
			Expect(mut).NotTo(Equal(data))
			_, e := DecodePrivateBundle(mut)
			Expect(e).To(HaveOccurred())
			Expect(e.Error()).To(Equal("invalid private migration bundle"))
		}
	})
	ginkgo.It("preserves all sidecars privately and marks restoration unsupported", func() {
		s := source(LocalAGIDefinition, `{"name":"k"}`)
		s.Sidecars = map[string][]byte{"state": []byte("s"), "character": []byte("c"), "scheduler": []byte("x"), "history": []byte("h"), "SECRET": []byte("SECRET")}
		p, r := analyze(s)
		Expect(r.Unsupported).To(BeNumerically(">=", 5))
		b := backed(p)
		Expect(b.wire.Source.Sidecars).To(Equal(s.Sidecars))
		Expect(fmt.Sprint(r)).NotTo(ContainSubstring("SECRET"))
		Expect(b.wire.NibPin).To(Equal("v0.12.1"))
	})
	ginkgo.It("accounts malformed MCP env children without exposing arbitrary keys", func() {
		p, r := analyze(source(LocalAIDefinition, `{"name":"SECRET","mcp_stdio_servers":"{\"mcpServers\":{\"SECRET\":{\"command\":\"SECRET\",\"env\":[{\"SECRET\":false}]}}}"}`))
		Expect(r.Invalid).To(BeNumerically(">", 0))
		Expect(r.Findings).To(ContainElement(HaveField("Path", "mcp_stdio_servers.mcpServers.unknown[1].env[0].unknown[1]")))
		Expect(p.wire.Definitions[0].Unsupported).To(ContainElement("mcp_stdio_servers.mcpServers.SECRET.env[0].SECRET"))
		for _, v := range []any{p, *p, r} {
			data, err := json.Marshal(v)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(data)).NotTo(ContainSubstring("SECRET"))
			Expect(fmt.Sprintf("%#v", v)).NotTo(ContainSubstring("SECRET"))
		}
	})
	ginkgo.It("binds backup to the exact plan and all source identity bytes", func() {
		s := source(LocalAIDefinition, `{"name":"k"}`)
		p, _ := analyze(s)
		backed(p)
		copy := *p
		_, err := Convert(&copy)
		Expect(err).To(HaveOccurred())
		_, err = Backup(context.Background(), &copy, &memorySink{})
		Expect(err).To(HaveOccurred())
		for _, change := range []func(*Source){
			func(s *Source) { s.Principal = "bob" },
			func(s *Source) { s.Format = LocalAGIDefinition },
			func(s *Source) { s.Sidecars = map[string][]byte{"state": []byte("SECRET")} },
		} {
			modified := s
			change(&modified)
			q, _ := analyze(modified)
			Expect(q.wire.Digest).NotTo(Equal(p.wire.Digest))
			_, err := Convert(q)
			Expect(err).To(HaveOccurred())
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		q, _ := analyze(s)
		_, err = Backup(ctx, q, &memorySink{})
		Expect(err).To(HaveOccurred())
		_, err = Convert(q)
		Expect(err).To(HaveOccurred())
	})
	ginkgo.It("allows concurrent plan backup and conversion without shared mutable outputs", func() {
		p, _ := analyze(source(LocalAIDefinition, `{"name":"k","api_key":"SECRET"}`))
		backed(p)
		var wg sync.WaitGroup
		for range 8 {
			wg.Go(func() {
				defer ginkgo.GinkgoRecover()
				_, err := Backup(context.Background(), p, &memorySink{})
				Expect(err).NotTo(HaveOccurred())
				bundle, err := Convert(p)
				Expect(err).NotTo(HaveOccurred())
				data := encoded(bundle)
				decoded, err := DecodePrivateBundle(data)
				Expect(err).NotTo(HaveOccurred())
				data[0] = '!'
				Expect(encoded(decoded)).To(Equal(encoded(bundle)))
			})
		}
		wg.Wait()
	})
	ginkgo.It("covers the pinned union catalog and repository source tags without runtime imports", func() {
		// Exact fixtures from AgentConfig: LocalAI 789ab14a and LocalAGIPin.
		// Each source has 65 fields; their union has 70, not 76.
		localAI := strings.Fields(`actions allowed_tools api_key api_url auto_compaction_threshold can_stop_itself cancel_previous_on_new_message connectors conversation_storage_mode description disable_sink_state dynamic_prompts enable_auto_compaction enable_evaluation enable_guided_tools enable_kb enable_kb_compaction enable_planning enable_reasoning enable_reasoning_for_instruct enable_reasoning_tool enable_skills excluded_tools filters initiate_conversations inner_monologue_template kb_as_tools kb_auto_search kb_compaction_interval kb_compaction_summarize kb_mode kb_results last_message_duration local_rag_api_key local_rag_url long_term_memory loop_detection max_attempts max_evaluation_loops max_iterations mcp_prepare_script mcp_servers mcp_stdio_servers model multimodal_model name parallel_jobs periodic_runs permanent_goal plan_reviewer_model required_tool_before_finish required_tool_before_finish_attempts required_tool_before_finish_prompt scheduler_poll_interval scheduler_task_template selected_skills skills_mode skills_prompt standalone_job strip_thinking_tags summary_long_term_memory system_prompt transcription_language transcription_model tts_model`)
		localAGI := strings.Fields(`actions allowed_tools api_key api_url auto_compaction_threshold can_stop_itself cancel_previous_on_new_message connectors conversation_storage_mode description disable_sink_state dynamic_prompts embedding_model enable_auto_compaction enable_evaluation enable_guided_tools enable_kb enable_kb_compaction enable_planning enable_reasoning enable_reasoning_tool enable_skills excluded_tools filters hud identity_guidance initiate_conversations inner_monologue_template kb_as_tools kb_auto_search kb_compaction_interval kb_compaction_summarize kb_results last_message_duration local_rag_api_key local_rag_url long_term_memory loop_detection max_attempts max_evaluation_loops mcp_prepare_script mcp_servers mcp_stdio_servers model multimodal_model name parallel_jobs periodic_runs permanent_goal plan_reviewer_model random_identity required_tool_before_finish required_tool_before_finish_attempts required_tool_before_finish_prompt reranker_model scheduler_poll_interval scheduler_task_template skills_prompt standalone_job strip_thinking_tags summary_long_term_memory system_prompt transcription_language transcription_model tts_model`)
		Expect(localAI).To(HaveLen(65))
		Expect(localAGI).To(HaveLen(65))
		union := map[string]bool{}
		for _, fields := range [][]string{localAI, localAGI} {
			for _, field := range fields {
				union[field] = true
			}
		}
		Expect(union).To(HaveLen(70))
		Expect(keys(catalog)).To(Equal(keys(union)))

		checkSource := func(path string, fixture []string) {
			data, err := os.ReadFile(path)
			Expect(err).NotTo(HaveOccurred(), path)
			text := string(data)
			start := strings.Index(text, "type AgentConfig struct {")
			Expect(start).To(BeNumerically(">=", 0), path)
			text = text[start:]
			end := strings.Index(text, "\n}")
			Expect(end).To(BeNumerically(">=", 0), path)
			fields := map[string]bool{}
			for _, match := range regexp.MustCompile(`json:"([^",]+)`).FindAllStringSubmatch(text[:end], -1) {
				fields[match[1]] = true
			}
			Expect(keys(fields)).To(Equal(fixture), path)
		}
		checkSource("../../agents/config.go", localAI)
		// Fixtures keep this test portable without fetching dependencies. When
		// the exact cached source exists, compare it too; never run its decoder.
		for _, cache := range []string{os.Getenv("GOMODCACHE"), "../../../../../../gomodcache"} {
			if cache == "" {
				continue
			}
			path := filepath.Join(cache, "github.com/mudler/!local!a!g!i@"+LocalAGIPin, "core/state/config.go")
			if _, err := os.Stat(path); os.IsNotExist(err) {
				continue
			}
			checkSource(path, localAGI)
		}
		for _, k := range keys(catalog) {
			kind := catalog[k]
			if k == "name" {
				continue // Covered by every fixture; do not construct duplicate keys.
			}
			v := `"x"`
			switch kind {
			case "bool", "cancel":
				v = "false"
			case "int":
				v = "0"
			case "list", "integration", "stdio":
				v = "null"
			}
			p, r := analyze(source(LocalAIDefinition, `{"name":"k","`+k+`":`+v+`}`))
			Expect(r.Invalid).To(BeZero(), k)
			Expect(p).NotTo(BeNil())
			Expect(r.Findings).To(ContainElement(HaveField("Path", k)))
		}
	})
})
