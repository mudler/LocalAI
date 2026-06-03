package meta_test

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	"github.com/mudler/LocalAI/core/config"
	"github.com/mudler/LocalAI/core/config/meta"
)

// TestAllFieldsHaveRegistryEntries fails when a NEW ModelConfig field
// is added without either a registry entry in DefaultRegistry() or an
// entry in the grandfatheredUnregistered baseline below.
//
// Why this matters: fields without a registry entry render in the UI
// with no description, the default `input` (single-line) component,
// and land in the catch-all "other" section — which is what we just
// hit for router.classifier_system_template. The reflection-based
// fallback produces something that works mechanically but is hostile
// to operators.
//
// How to fix when this test fails:
//
//  1. Preferred — add a registry entry to DefaultRegistry() in
//     registry.go with Section, Label, Description, and Component.
//     See e.g. "router.classifier" or "template.chat" for the pattern.
//
//  2. Escape hatch — append the field path to
//     grandfatheredUnregistered with a one-line comment justifying
//     why it has no UI surface (internal, deprecated, legacy
//     compatibility shim, etc.). The expectation is that this list
//     shrinks over time as fields get proper registry entries; it
//     should never grow without good reason.
//
// The grandfathered list was seeded from a one-time audit. Migrating
// the existing 150+ entries to proper registry metadata is out of
// scope for any single PR; the lock just stops the list from growing.
func TestAllFieldsHaveRegistryEntries(t *testing.T) {
	g := NewWithT(t)
	md := meta.BuildConfigMetadata(reflect.TypeOf(config.ModelConfig{}))
	reg := meta.DefaultRegistry()

	grand := make(map[string]struct{}, len(grandfatheredUnregistered))
	for _, p := range grandfatheredUnregistered {
		grand[p] = struct{}{}
	}

	var missing []string
	for _, f := range md.Fields {
		if _, ok := reg[f.Path]; ok {
			continue
		}
		if _, ok := grand[f.Path]; ok {
			continue
		}
		missing = append(missing, f.Path)
	}

	sort.Strings(missing)
	g.Expect(missing).To(BeEmpty(),
		"%d config field(s) have no registry entry and are not on the grandfathered list.\n"+
			"Add a registry entry to core/config/meta/registry.go OR append to grandfatheredUnregistered in this file with a justification:\n  %s",
		len(missing), strings.Join(missing, "\n  "),
	)

	// Inverse drift check: catch dead entries on the grandfathered
	// list (field was renamed/removed, or someone wrote a registry
	// entry without trimming the grandfathered duplicate).
	known := make(map[string]struct{}, len(md.Fields))
	for _, f := range md.Fields {
		known[f.Path] = struct{}{}
	}
	var stale, duplicated []string
	for _, p := range grandfatheredUnregistered {
		if _, ok := known[p]; !ok {
			stale = append(stale, p)
		}
		if _, ok := reg[p]; ok {
			duplicated = append(duplicated, p)
		}
	}
	sort.Strings(stale)
	g.Expect(stale).To(BeEmpty(),
		"grandfatheredUnregistered references fields that no longer exist in ModelConfig — remove them:\n  %s",
		strings.Join(stale, "\n  "))
	sort.Strings(duplicated)
	g.Expect(duplicated).To(BeEmpty(),
		"grandfatheredUnregistered references fields that now HAVE a registry entry — remove them so the test stays meaningful:\n  %s",
		strings.Join(duplicated, "\n  "))
}

// grandfatheredUnregistered is the baseline of config fields that
// pre-date the registry-coverage test and have no UI metadata yet.
// Adding new entries here should be a deliberate, justified decision
// — prefer adding a registry entry in registry.go instead.
//
// Keep the list sorted (one-line-per-entry) so the diff is minimal
// when an entry is removed or (rarely) added.
var grandfatheredUnregistered = []string{
	"agent.disable_sink_state",
	"agent.enable_mcp_prompts",
	"agent.enable_plan_re_evaluator",
	"agent.enable_planning",
	"agent.enable_reasoning",
	"agent.force_reasoning_tool",
	"agent.loop_detection",
	"agent.max_adjustment_attempts",
	"agent.max_attempts",
	"agent.max_iterations",
	"cfg_scale",
	"concurrency_groups",
	"cutstrings",
	"debug",
	"diffusers.clip_model",
	"diffusers.clip_skip",
	"diffusers.clip_subfolder",
	"diffusers.control_net",
	"diffusers.enable_parameters",
	"diffusers.img2img",
	"disable_log_stats",
	"disabled",
	"download_files",
	"draft_model",
	"dtype",
	"enforce_eager",
	"engine_args",
	"extract_regex",
	"feature_flags",
	"function.argument_regex",
	"function.argument_regex_key_name",
	"function.argument_regex_value_name",
	"function.automatic_tool_parsing_fallback",
	"function.capture_llm_results",
	"function.disable_no_action",
	"function.disable_peg_parser",
	"function.function_arguments_key",
	"function.function_name_key",
	"function.grammar.disable_parallel_new_lines",
	"function.grammar.expect_strings_after_json",
	"function.grammar.no_mixed_free_string",
	"function.grammar.prefix",
	"function.grammar.properties_order",
	"function.grammar.schema_type",
	"function.grammar.triggers",
	"function.json_regex_match",
	"function.no_action_description_name",
	"function.no_action_function_name",
	"function.replace_function_results",
	"function.replace_llm_results",
	"function.response_regex",
	"function.xml_format.allow_toolcall_in_think",
	"function.xml_format.key_start",
	"function.xml_format.key_val_sep",
	"function.xml_format.key_val_sep2",
	"function.xml_format.last_tool_end",
	"function.xml_format.last_val_end",
	"function.xml_format.raw_argval",
	"function.xml_format.scope_end",
	"function.xml_format.scope_start",
	"function.xml_format.tool_end",
	"function.xml_format.tool_sep",
	"function.xml_format.tool_start",
	"function.xml_format.trim_raw_argval",
	"function.xml_format.val_end",
	"function.xml_format_preset",
	"gpu_memory_utilization",
	"grammar",
	"grpc.attempts",
	"grpc.attempts_sleep_time",
	"limit_mm_per_prompt.audio",
	"limit_mm_per_prompt.image",
	"limit_mm_per_prompt.video",
	"limits.max_concurrent",
	"limits.retry_after_seconds",
	"load_format",
	"lora_adapter",
	"lora_adapters",
	"lora_base",
	"lora_scale",
	"lora_scales",
	"main_gpu",
	"max_model_len",
	"mcp.remote",
	"mcp.stdio",
	"mirostat",
	"mirostat_eta",
	"mirostat_tau",
	"mmproj",
	"n_draft",
	"ngqa",
	"no_kv_offloading",
	"no_mulmatq",
	"numa",
	"options",
	"overrides",
	"parameters.batch",
	"parameters.clip_skip",
	"parameters.echo",
	"parameters.encoding_format",
	"parameters.frequency_penalty",
	"parameters.ignore_eos",
	"parameters.language",
	"parameters.logit_bias",
	"parameters.logprobs",
	"parameters.min_p",
	"parameters.model",
	"parameters.n",
	"parameters.n_keep",
	"parameters.negative_prompt",
	"parameters.negative_prompt_scale",
	"parameters.presence_penalty",
	"parameters.repeat_last_n",
	"parameters.rope_freq_base",
	"parameters.rope_freq_scale",
	"parameters.tfz",
	"parameters.tokenizer",
	"parameters.top_logprobs",
	"parameters.translate",
	"parameters.typical_p",
	// Deprecated PII keys kept only as untyped shadows so old YAMLs still
	// parse; Validate() warns. No UI surface — use pii.detectors +
	// pii_detection. Removed next release.
	"pii.ner",
	"pii.patterns",
	"pinned",
	"prompt_cache_all",
	"prompt_cache_path",
	"prompt_cache_ro",
	"proxy.api_key_file",
	"reasoning.disable",
	"reasoning.disable_reasoning_tag_prefill",
	"reasoning.strip_reasoning_only",
	"reasoning.tag_pairs",
	"reasoning.thinking_start_tokens",
	"reranking",
	"rms_norm_eps",
	"roles",
	"rope_scaling",
	"step",
	"stopwords",
	"swap_space",
	"system_prompt",
	"template.edit",
	"template.join_chat_messages_by_character",
	"template.multimodal",
	"template.reply_prefix",
	"tensor_parallel_size",
	"tensor_split",
	"trimspace",
	"trimsuffix",
	"trust_remote_code",
	"tts.audio_path",
	"type",
	"yarn_attn_factor",
	"yarn_beta_fast",
	"yarn_beta_slow",
	"yarn_ext_factor",
}
