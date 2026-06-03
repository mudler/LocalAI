package config

import (
	"io"
	"net/http"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"gopkg.in/yaml.v3"
)

var _ = Describe("Test cases for config related functions", func() {
	Context("ModelID", func() {
		It("returns Name when set", func() {
			c := ModelConfig{Name: "my-name"}
			c.Model = "my-model"
			Expect(c.ModelID()).To(Equal("my-name"))
		})
		It("falls back to Model when Name is empty", func() {
			c := ModelConfig{}
			c.Model = "my-model"
			Expect(c.ModelID()).To(Equal("my-model"))
		})
		It("returns empty string when both are empty", func() {
			c := ModelConfig{}
			Expect(c.ModelID()).To(Equal(""))
		})
	})

	Context("Test Read configuration functions", func() {
		It("Test Validate", func() {
			tmp, err := os.CreateTemp("", "config.yaml")
			Expect(err).To(BeNil())
			defer os.Remove(tmp.Name())
			_, err = tmp.WriteString(
				`backend: "../foo-bar"
name: "foo"
parameters:
  model: "foo-bar"
known_usecases:
- chat
- COMPLETION
`)
			Expect(err).ToNot(HaveOccurred())
			configs, err := readModelConfigsFromFile(tmp.Name())
			config := configs[0]
			Expect(err).To(BeNil())
			Expect(config).ToNot(BeNil())
			valid, err := config.Validate()
			Expect(err).To(HaveOccurred())
			Expect(valid).To(BeFalse())
			Expect(config.KnownUsecases).ToNot(BeNil())
		})
		It("Test Validate", func() {
			tmp, err := os.CreateTemp("", "config.yaml")
			Expect(err).To(BeNil())
			defer os.Remove(tmp.Name())
			_, err = tmp.WriteString(
				`name: bar-baz
backend: "foo-bar"
parameters:
  model: "foo-bar"`)
			Expect(err).ToNot(HaveOccurred())
			configs, err := readModelConfigsFromFile(tmp.Name())
			config := configs[0]
			Expect(err).To(BeNil())
			Expect(config).ToNot(BeNil())
			// two configs in config.yaml
			Expect(config.Name).To(Equal("bar-baz"))
			valid, err := config.Validate()
			Expect(err).To(BeNil())
			Expect(valid).To(BeTrue())

			// llama-cpp configs can't mix the score usecase with
			// chat/completion/embeddings — Score bypasses the slot
			// loop and would race the llama_context. The check fires
			// at load and save time; here we exercise it directly.
			scoreFlag := FLAG_SCORE | FLAG_CHAT
			conflicting := ModelConfig{
				Name:          "router-but-also-chat",
				Backend:       "llama-cpp",
				KnownUsecases: &scoreFlag,
			}
			valid, err = conflicting.Validate()
			Expect(valid).To(BeFalse())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("score is incompatible"))

			scoreOnly := FLAG_SCORE
			dedicated := ModelConfig{
				Name:          "router-only",
				Backend:       "llama-cpp",
				KnownUsecases: &scoreOnly,
			}
			valid, err = dedicated.Validate()
			Expect(valid).To(BeTrue())
			Expect(err).NotTo(HaveOccurred())

			// The constraint is llama-cpp-specific; other backends
			// may safely combine.
			scoreAndChat := FLAG_SCORE | FLAG_CHAT
			otherBackend := ModelConfig{
				Name:          "vllm-router-and-chat",
				Backend:       "vllm",
				KnownUsecases: &scoreAndChat,
			}
			valid, err = otherBackend.Validate()
			Expect(valid).To(BeTrue())
			Expect(err).NotTo(HaveOccurred())

			// token_classify on llama-cpp also bypasses the slot loop, so
			// it can't mix with chat/completion — but unlike score it
			// REQUIRES embeddings (TOKEN_CLS pooling), so embeddings is not
			// a conflict.
			tcAndChat := FLAG_TOKEN_CLASSIFY | FLAG_CHAT
			tcConflicting := ModelConfig{
				Name:          "ner-but-also-chat",
				Backend:       "llama-cpp",
				KnownUsecases: &tcAndChat,
			}
			valid, err = tcConflicting.Validate()
			Expect(valid).To(BeFalse())
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("token_classify is incompatible"))

			tcAndEmbeddings := FLAG_TOKEN_CLASSIFY | FLAG_EMBEDDINGS
			tcWithEmbeddings := ModelConfig{
				Name:          "pii-ner",
				Backend:       "llama-cpp",
				KnownUsecases: &tcAndEmbeddings,
			}
			valid, err = tcWithEmbeddings.Validate()
			Expect(valid).To(BeTrue())
			Expect(err).NotTo(HaveOccurred())

			// Cloud-proxy: api_key_env and api_key_file are mutually
			// exclusive — picking both is a config bug we catch at
			// load/save rather than at backend-load time.
			bothKeys := ModelConfig{
				Name:    "both-keys",
				Backend: "cloud-proxy",
				Proxy: ProxyConfig{
					UpstreamURL: "https://example.com/v1",
					APIKeyEnv:   "OPENAI_KEY",
					APIKeyFile:  "/run/secrets/openai",
				},
			}
			valid, err = bothKeys.Validate()
			Expect(valid).To(BeFalse())
			Expect(err).To(MatchError(ContainSubstring("mutually exclusive")))

			// Translate mode requires a provider — without one, the
			// backend has no way to pick a wire format.
			translateNoProvider := ModelConfig{
				Name:    "translate-no-provider",
				Backend: "cloud-proxy",
				Proxy:   ProxyConfig{UpstreamURL: "https://example.com/v1", Mode: ProxyModeTranslate},
			}
			valid, err = translateNoProvider.Validate()
			Expect(valid).To(BeFalse())
			Expect(err).To(MatchError(ContainSubstring("translate mode requires provider")))

			// Unknown mode is rejected.
			badMode := ModelConfig{
				Name:    "bad-mode",
				Backend: "cloud-proxy",
				Proxy:   ProxyConfig{UpstreamURL: "https://example.com/v1", Mode: "rewrite"},
			}
			valid, err = badMode.Validate()
			Expect(valid).To(BeFalse())
			Expect(err).To(MatchError(ContainSubstring("unknown mode")))

			// Passthrough (default) with one key source is happy.
			passthroughOK := ModelConfig{
				Name:    "passthrough-ok",
				Backend: "cloud-proxy",
				Proxy:   ProxyConfig{UpstreamURL: "https://example.com/v1", APIKeyEnv: "OPENAI_KEY"},
			}
			valid, err = passthroughOK.Validate()
			Expect(valid).To(BeTrue())
			Expect(err).NotTo(HaveOccurred())

			// router.score_normalization: load-time rejection of an
			// unknown value. The classifier consumes it lazily, so
			// without this validation a YAML typo wouldn't surface
			// until the first router request panicked deep in
			// NewScoreClassifier.
			badNorm := ModelConfig{
				Name: "bad-norm",
				Router: RouterConfig{
					ScoreNormalization: "men", // typo of "mean"
				},
			}
			valid, err = badNorm.Validate()
			Expect(valid).To(BeFalse())
			Expect(err).To(MatchError(ContainSubstring("unknown score_normalization")))

			// Accepted values pass.
			for _, mode := range []string{"", ScoreNormalizationRaw, ScoreNormalizationMean} {
				goodNorm := ModelConfig{
					Name:   "good-norm-" + mode,
					Router: RouterConfig{ScoreNormalization: mode},
				}
				valid, err = goodNorm.Validate()
				Expect(valid).To(BeTrue(), "score_normalization=%q should be accepted", mode)
				Expect(err).NotTo(HaveOccurred())
			}

			// router.classifier_system_template: parse-time rejection
			// of malformed Go templates. Same reasoning as above —
			// without this the parse error wouldn't surface until
			// the first router request panicked in NewScoreClassifier.
			badTmpl := ModelConfig{
				Name: "bad-tmpl",
				Router: RouterConfig{
					ClassifierSystemTemplate: "Routes: {{range .Policies",
				},
			}
			valid, err = badTmpl.Validate()
			Expect(valid).To(BeFalse())
			Expect(err).To(MatchError(ContainSubstring("classifier_system_template parse error")))

			// Well-formed template passes.
			goodTmpl := ModelConfig{
				Name: "good-tmpl",
				Router: RouterConfig{
					ClassifierSystemTemplate: `Routes: {{range .Policies}}{{.Label}} {{end}}`,
				},
			}
			valid, err = goodTmpl.Validate()
			Expect(valid).To(BeTrue())
			Expect(err).NotTo(HaveOccurred())

			// download https://raw.githubusercontent.com/mudler/LocalAI/v2.25.0/embedded/models/hermes-2-pro-mistral.yaml
			httpClient := http.Client{}
			resp, err := httpClient.Get("https://raw.githubusercontent.com/mudler/LocalAI/v2.25.0/embedded/models/hermes-2-pro-mistral.yaml")
			Expect(err).To(BeNil())
			defer resp.Body.Close()
			tmp, err = os.CreateTemp("", "config.yaml")
			Expect(err).To(BeNil())
			defer os.Remove(tmp.Name())
			_, err = io.Copy(tmp, resp.Body)
			Expect(err).To(BeNil())
			configs, err = readModelConfigsFromFile(tmp.Name())
			config = configs[0]
			Expect(err).To(BeNil())
			Expect(config).ToNot(BeNil())
			// two configs in config.yaml
			Expect(config.Name).To(Equal("hermes-2-pro-mistral"))
			valid, err = config.Validate()
			Expect(err).To(BeNil())
			Expect(valid).To(BeTrue())
		})
	})
	It("Properly handles backend usecase matching", func() {
		a := ModelConfig{
			Name: "a",
		}
		Expect(a.HasUsecases(FLAG_ANY)).To(BeTrue()) // FLAG_ANY just means the config _exists_ essentially.

		b := ModelConfig{
			Name:    "b",
			Backend: "stablediffusion",
		}
		Expect(b.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(b.HasUsecases(FLAG_IMAGE)).To(BeTrue())
		Expect(b.HasUsecases(FLAG_CHAT)).To(BeFalse())

		c := ModelConfig{
			Name:    "c",
			Backend: "llama-cpp",
			TemplateConfig: TemplateConfig{
				Chat: "chat",
			},
		}
		Expect(c.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(c.HasUsecases(FLAG_IMAGE)).To(BeFalse())
		Expect(c.HasUsecases(FLAG_COMPLETION)).To(BeFalse())
		Expect(c.HasUsecases(FLAG_CHAT)).To(BeTrue())

		d := ModelConfig{
			Name:    "d",
			Backend: "llama-cpp",
			TemplateConfig: TemplateConfig{
				Chat:       "chat",
				Completion: "completion",
			},
		}
		Expect(d.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(d.HasUsecases(FLAG_IMAGE)).To(BeFalse())
		Expect(d.HasUsecases(FLAG_COMPLETION)).To(BeTrue())
		Expect(d.HasUsecases(FLAG_CHAT)).To(BeTrue())

		trueValue := true
		e := ModelConfig{
			Name:    "e",
			Backend: "llama-cpp",
			TemplateConfig: TemplateConfig{
				Completion: "completion",
			},
			Embeddings: &trueValue,
		}

		Expect(e.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(e.HasUsecases(FLAG_IMAGE)).To(BeFalse())
		Expect(e.HasUsecases(FLAG_COMPLETION)).To(BeTrue())
		Expect(e.HasUsecases(FLAG_CHAT)).To(BeFalse())
		Expect(e.HasUsecases(FLAG_EMBEDDINGS)).To(BeTrue())

		// Router models are chat dispatchers: no chat template of their
		// own, but invoked through the chat endpoint, so they default to
		// chat-capable.
		r := ModelConfig{
			Name: "r",
			Router: RouterConfig{
				Candidates: []RouterCandidate{{Model: "downstream", Labels: []string{"general"}}},
			},
		}
		Expect(r.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(r.HasUsecases(FLAG_CHAT)).To(BeTrue())

		f := ModelConfig{
			Name:    "f",
			Backend: "piper",
		}
		Expect(f.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(f.HasUsecases(FLAG_TTS)).To(BeTrue())
		Expect(f.HasUsecases(FLAG_CHAT)).To(BeFalse())

		g := ModelConfig{
			Name:    "g",
			Backend: "whisper",
		}
		Expect(g.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(g.HasUsecases(FLAG_TRANSCRIPT)).To(BeTrue())
		Expect(g.HasUsecases(FLAG_TTS)).To(BeFalse())

		h := ModelConfig{
			Name:    "h",
			Backend: "transformers-musicgen",
		}
		Expect(h.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(h.HasUsecases(FLAG_TRANSCRIPT)).To(BeFalse())
		Expect(h.HasUsecases(FLAG_TTS)).To(BeTrue())
		Expect(h.HasUsecases(FLAG_SOUND_GENERATION)).To(BeTrue())

		knownUsecases := FLAG_CHAT | FLAG_COMPLETION
		i := ModelConfig{
			Name:    "i",
			Backend: "whisper",
			// Earlier test checks parsing, this just needs to set final values
			KnownUsecases: &knownUsecases,
		}
		Expect(i.HasUsecases(FLAG_ANY)).To(BeTrue())
		Expect(i.HasUsecases(FLAG_TRANSCRIPT)).To(BeTrue())
		Expect(i.HasUsecases(FLAG_TTS)).To(BeFalse())
		Expect(i.HasUsecases(FLAG_COMPLETION)).To(BeTrue())
		Expect(i.HasUsecases(FLAG_CHAT)).To(BeTrue())

		// Declared `known_usecases: [score]` is authoritative — the
		// guessing heuristic must NOT add chat on top, even though the
		// inherited chatml template would otherwise satisfy the chat
		// heuristic. Score means "this model is reserved for the
		// router classifier"; surfacing it as a chat model defeats the
		// reservation and reintroduces the slot contention the load-time
		// score/chat conflict check exists to prevent.
		scoreReserved := FLAG_SCORE
		j := ModelConfig{
			Name:          "arch-router",
			Backend:       "llama-cpp",
			KnownUsecases: &scoreReserved,
			TemplateConfig: TemplateConfig{
				Chat:        "inherited from chatml",
				ChatMessage: "inherited from chatml",
				Completion:  "inherited from chatml",
			},
		}
		Expect(j.HasUsecases(FLAG_SCORE)).To(BeTrue())
		Expect(j.HasUsecases(FLAG_CHAT)).To(BeFalse())
		Expect(j.HasUsecases(FLAG_COMPLETION)).To(BeFalse())
		Expect(j.HasUsecases(FLAG_EMBEDDINGS)).To(BeFalse())

		// Declared `known_usecases: [token_classify]` is likewise
		// authoritative — a PII NER model is reserved for the redactor's
		// NER tier and must not surface as chat or as a general embeddings
		// model, even though it loads with embeddings enabled (its
		// TOKEN_CLS head produces BIOES logits, not reusable embeddings).
		tcReserved := FLAG_TOKEN_CLASSIFY
		embTrue := true
		k := ModelConfig{
			Name:          "privacy-filter",
			Backend:       "llama-cpp",
			KnownUsecases: &tcReserved,
			Embeddings:    &embTrue,
			TemplateConfig: TemplateConfig{
				Chat:        "inherited from chatml",
				ChatMessage: "inherited from chatml",
			},
		}
		Expect(k.HasUsecases(FLAG_TOKEN_CLASSIFY)).To(BeTrue())
		Expect(k.HasUsecases(FLAG_CHAT)).To(BeFalse())
		Expect(k.HasUsecases(FLAG_EMBEDDINGS)).To(BeFalse())
	})
	It("Test Validate with invalid MCP config", func() {
		tmp, err := os.CreateTemp("", "config.yaml")
		Expect(err).To(BeNil())
		defer os.Remove(tmp.Name())
		_, err = tmp.WriteString(
			`name: test-mcp
backend: "llama-cpp"
mcp:
  stdio: |
    {
      "mcpServers": {
        "ddg": {
          "command": "/docker/docker",
          "args": ["run", "-i"]
        }
        "weather": {
          "command": "/docker/docker",
          "args": ["run", "-i"]
        }
      }
    }`)
		Expect(err).ToNot(HaveOccurred())
		configs, err := readModelConfigsFromFile(tmp.Name())
		config := configs[0]
		Expect(err).To(BeNil())
		Expect(config).ToNot(BeNil())
		valid, err := config.Validate()
		Expect(err).To(HaveOccurred())
		Expect(valid).To(BeFalse())
		Expect(err.Error()).To(ContainSubstring("invalid MCP configuration"))
	})
	It("Test Validate with valid MCP config", func() {
		tmp, err := os.CreateTemp("", "config.yaml")
		Expect(err).To(BeNil())
		defer os.Remove(tmp.Name())
		_, err = tmp.WriteString(
			`name: test-mcp-valid
backend: "llama-cpp"
mcp:
  stdio: |
    {
      "mcpServers": {
        "ddg": {
          "command": "/docker/docker",
          "args": ["run", "-i"]
        },
        "weather": {
          "command": "/docker/docker",
          "args": ["run", "-i"]
        }
      }
    }`)
		Expect(err).ToNot(HaveOccurred())
		configs, err := readModelConfigsFromFile(tmp.Name())
		config := configs[0]
		Expect(err).To(BeNil())
		Expect(config).ToNot(BeNil())
		valid, err := config.Validate()
		Expect(err).To(BeNil())
		Expect(valid).To(BeTrue())
	})
	It("Test Validate rejects unmarshalable engine_args", func() {
		// chan values cannot be JSON-marshalled. A valid YAML config could
		// not produce one, but a Go caller stuffing a bad value would, and
		// silently dropping it would change runtime behaviour.
		cfg := &ModelConfig{
			Backend: "vllm",
			LLMConfig: LLMConfig{
				EngineArgs: map[string]any{
					"speculative_config": make(chan int),
				},
			},
		}
		valid, err := cfg.Validate()
		Expect(valid).To(BeFalse())
		Expect(err).ToNot(BeNil())
		Expect(err.Error()).To(ContainSubstring("engine_args is not JSON-serialisable"))
	})
	It("Test Validate accepts well-formed engine_args", func() {
		cfg := &ModelConfig{
			Backend: "vllm",
			LLMConfig: LLMConfig{
				EngineArgs: map[string]any{
					"data_parallel_size": 8,
					"speculative_config": map[string]any{
						"method":                 "ngram",
						"num_speculative_tokens": 4,
					},
				},
			},
		}
		valid, err := cfg.Validate()
		Expect(err).To(BeNil())
		Expect(valid).To(BeTrue())
	})
	Context("ConcurrencyGroups", func() {
		It("returns nil when no groups are configured", func() {
			cfg := &ModelConfig{Name: "no-groups"}
			Expect(cfg.GetConcurrencyGroups()).To(BeNil())
		})
		It("returns nil when all entries are blank", func() {
			cfg := &ModelConfig{
				Name:              "blanks",
				ConcurrencyGroups: []string{"", "   ", "\t"},
			}
			Expect(cfg.GetConcurrencyGroups()).To(BeNil())
		})
		It("trims whitespace, drops empty entries, and dedupes", func() {
			cfg := &ModelConfig{
				Name:              "messy",
				ConcurrencyGroups: []string{" vram-heavy ", "", "vram-heavy", "vision", "  vision "},
			}
			Expect(cfg.GetConcurrencyGroups()).To(Equal([]string{"vram-heavy", "vision"}))
		})
		It("returns a defensive copy", func() {
			cfg := &ModelConfig{
				Name:              "copy",
				ConcurrencyGroups: []string{"heavy"},
			}
			got := cfg.GetConcurrencyGroups()
			got[0] = "tampered"
			Expect(cfg.GetConcurrencyGroups()).To(Equal([]string{"heavy"}))
		})
		It("parses concurrency_groups from YAML", func() {
			tmp, err := os.CreateTemp("", "concgroups.yaml")
			Expect(err).To(BeNil())
			defer func() { _ = os.Remove(tmp.Name()) }()
			_, err = tmp.WriteString(
				`name: heavy-a
backend: llama-cpp
parameters:
  model: heavy-a.gguf
concurrency_groups:
  - vram-heavy
  - "120b"
`)
			Expect(err).ToNot(HaveOccurred())
			configs, err := readModelConfigsFromFile(tmp.Name())
			Expect(err).To(BeNil())
			Expect(configs).To(HaveLen(1))
			Expect(configs[0].ConcurrencyGroups).To(Equal([]string{"vram-heavy", "120b"}))
			Expect(configs[0].GetConcurrencyGroups()).To(Equal([]string{"vram-heavy", "120b"}))
		})
	})

	// When templating is delegated to the backend (use_tokenizer_template),
	// the backend also owns tool-call grammar generation and parsing. A
	// LocalAI-generated grammar sent alongside would override the backend's
	// native (name-first) tool pipeline and make it stream the tool-call JSON
	// back as plain content (issue #10052). SetDefaults must therefore couple
	// the two: tokenizer template implies grammar generation is disabled.
	Context("use_tokenizer_template couples with grammar disable (issue #10052)", func() {
		It("disables Go grammar generation when the tokenizer template is used", func() {
			cfg := &ModelConfig{
				TemplateConfig: TemplateConfig{UseTokenizerTemplate: true},
			}
			Expect(cfg.FunctionsConfig.GrammarConfig.NoGrammar).To(BeFalse())

			cfg.SetDefaults()

			Expect(cfg.FunctionsConfig.GrammarConfig.NoGrammar).To(BeTrue(),
				"use_tokenizer_template must imply grammar.disable so tools go to the backend's native pipeline")
		})

		It("leaves grammar generation enabled when the tokenizer template is not used", func() {
			cfg := &ModelConfig{}

			cfg.SetDefaults()

			Expect(cfg.FunctionsConfig.GrammarConfig.NoGrammar).To(BeFalse(),
				"models that template in Go still rely on the Go-generated grammar")
		})
	})
})

var _ = Describe("PII config accessors", func() {
	It("PIIDetectors returns a fresh copy of the consumer's detector list", func() {
		cfg := &ModelConfig{PII: PIIConfig{Detectors: []string{"a", "b"}}}
		got := cfg.PIIDetectors()
		Expect(got).To(Equal([]string{"a", "b"}))
		got[0] = "mutated"
		Expect(cfg.PII.Detectors[0]).To(Equal("a"), "accessor must not alias the underlying slice")
	})

	It("PIIDetectors is nil when none are configured", func() {
		Expect((&ModelConfig{}).PIIDetectors()).To(BeNil())
	})

	It("exposes the detector model's pii_detection policy", func() {
		cfg := &ModelConfig{PIIDetection: PIIDetectionConfig{
			MinScore:      0.5,
			DefaultAction: "mask",
			EntityActions: map[string]string{"PASSWORD": "block", "EMAIL": "mask"},
		}}
		Expect(cfg.PIIDetectionMinScore()).To(BeNumerically("~", 0.5, 1e-6))
		Expect(cfg.PIIDetectionDefaultAction()).To(Equal("mask"))
		ea := cfg.PIIDetectionEntityActions()
		Expect(ea).To(HaveKeyWithValue("PASSWORD", "block"))
		ea["PASSWORD"] = "mutated"
		Expect(cfg.PIIDetection.EntityActions["PASSWORD"]).To(Equal("block"), "accessor must return a fresh map")
	})

	It("unmarshals pii.detectors and pii_detection from YAML", func() {
		var cfg ModelConfig
		raw := []byte("name: consumer\npii:\n  enabled: true\n  detectors: [pf]\npii_detection:\n  min_score: 0.4\n  default_action: mask\n  entity_actions:\n    PASSWORD: block\n")
		Expect(yaml.Unmarshal(raw, &cfg)).To(Succeed())
		Expect(cfg.PIIDetectors()).To(Equal([]string{"pf"}))
		Expect(cfg.PIIDetectionDefaultAction()).To(Equal("mask"))
		Expect(cfg.PIIDetectionEntityActions()).To(HaveKeyWithValue("PASSWORD", "block"))
	})
})
