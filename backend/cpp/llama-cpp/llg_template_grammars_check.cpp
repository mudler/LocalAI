// Check of patches/0008-llguidance-template-grammars.patch against every chat template of the
// llama.cpp tree.
//
// For each vocab given and each template, it builds the tool-call grammar of the template for a
// fixed tool set (the path of the server: chat session, then the trigger conversion), translates it
// to llguidance, compiles it, and replays a few outputs token by token on llguidance and on GBNF:
//
//   free    plain text with no tool call
//   call    the tool call that the template renders for an assistant message
//   glued   text with the call right after it, so the trigger can start inside a token
//   bad     the call with an argument name that is not in the schema
//   think   the call after reasoning, when the template renders reasoning
//
// GBNF is the reference: at every token both engines must accept or reject the same token, and
// agree whether the output may end. llguidance is checked twice, with the one-token fast path and
// with the full mask. Halfway through each replay the sampler is cloned and restored, as
// speculative decoding does, and the rest must give the same decisions.
//
// A template fails when its grammar does not translate, does not compile, or an engine disagrees.
// A template with no tool grammar, or that fails on GBNF too, is reported and skipped.
//
// usage: llg_template_grammars_check TEMPLATES_DIR VOCAB.gguf [VOCAB.gguf...]
// Needs a llama.cpp build with LLAMA_LLGUIDANCE and the patch. Exit code 1 on any failure.

#include "chat.h"
#include "common.h"
#include "gbnf-to-lark.h"
#include "llama.h"
#include "log.h"
#include "sampling.h"

#include <algorithm>
#include <chrono>
#include <cmath>
#include <cstdio>
#include <filesystem>
#include <fstream>
#include <map>
#include <sstream>
#include <string>
#include <vector>

namespace fs = std::filesystem;

using clk = std::chrono::steady_clock;

static common_chat_tool make_tool(const std::string & name, const std::string & desc, const std::string & params) {
    common_chat_tool t;
    t.name        = name;
    t.description = desc;
    t.parameters  = params;
    return t;
}

static std::vector<common_chat_tool> tool_set() {
    return {
        make_tool("get_weather", "Get the current weather in a city",
                  R"({"type":"object","properties":{"location":{"type":"string","description":"City name"},)"
                  R"("unit":{"type":"string","enum":["celsius","fahrenheit"]}},"required":["location"]})"),
        make_tool("search_docs", "Search the documentation",
                  R"({"type":"object","properties":{"query":{"type":"string"},"max_results":{"type":"integer"}},)"
                  R"("required":["query"]})"),
    };
}

struct engines {
    llama_sampler * llg  = nullptr;
    llama_sampler * gbnf = nullptr;
};

// one decision: is the token allowed (single-token check, as common_sampler_sample does first)
static bool allowed_one(llama_sampler * s, llama_token t) {
    llama_token_data       d   = { t, 1.0f, 0.0f };
    llama_token_data_array arr = { &d, 1, -1, false };
    llama_sampler_apply(s, &arr);
    return d.logit != -INFINITY;
}

// the same decision through a mask over the whole vocab
static bool allowed_mask(llama_sampler * s, llama_token t, int n_vocab, std::vector<llama_token_data> & cand) {
    cand.resize(n_vocab);
    for (int v = 0; v < n_vocab; v++) {
        cand[v] = { v, 0.0f, 0.0f };
    }
    llama_token_data_array arr = { cand.data(), (size_t) n_vocab, -1, false };
    llama_sampler_apply(s, &arr);
    return cand[t].logit != -INFINITY;
}

struct replay_result {
    std::string status = "OK"; // OK, MISMATCH
    std::string detail;
    int         gbnf_reject = -1; // first token GBNF rejects, -1 for none
    size_t      n_tokens    = 0;
    int         noncanonical = 0; // tokens the one-token check accepts and the mask leaves out
    bool        on_gbnf = false;  // the sampler moved to GBNF (llguidance rejected a token GBNF accepts)
};

// Replays `tokens` from index `from` on both engines. Returns the decisions as a string ('a' accept,
// 'r' reject) and sets res on the first disagreement.
static std::string replay_from(const llama_vocab * vocab, engines & e, const std::vector<llama_token> & tokens,
                               size_t from, bool check_mask, replay_result & res, std::vector<llama_token_data> & cand,
                               const std::string & tag) {
    const int   n_vocab = llama_vocab_n_tokens(vocab);
    std::string decisions;
    std::string text;
    for (size_t i = from; i < tokens.size(); i++) {
        const llama_token t   = tokens[i];
        const bool        g   = allowed_one(e.gbnf, t);
        const bool        l   = allowed_one(e.llg, t);
        const bool        lm  = check_mask ? allowed_mask(e.llg, t, n_vocab, cand) : l;
        decisions += g ? 'a' : 'r';
        if (l && !lm) {
            // llguidance's mask holds only the canonical tokenization of text the grammar forces;
            // the one-token check accepts any token whose bytes fit, as GBNF does
            res.noncanonical++;
        }
        if (!l && lm && res.status == "OK") {
            res.status = "MISMATCH";
            res.detail = tag + " fast path " + (l ? "accepts" : "rejects") + " but mask " + (lm ? "accepts" : "rejects") +
                         " token " + std::to_string(i) + " '" + common_token_to_piece(vocab, t, true) + "' after '" + text + "'";
        }
        if (g != l && res.status == "OK") {
            res.status = "MISMATCH";
            res.detail = tag + " GBNF " + (g ? "accepts" : "rejects") + ", llguidance " + (l ? "accepts" : "rejects") +
                         " token " + std::to_string(i) + " '" + common_token_to_piece(vocab, t, true) + "' after '" + text + "'";
        }
        if (!g || !l) {
            if (!g && res.gbnf_reject < 0) {
                res.gbnf_reject = (int) i;
            }
            return decisions;
        }
        if (llama_vocab_is_eog(vocab, t)) {
            return decisions; // the output ends here
        }
        llama_sampler_accept(e.gbnf, t);
        llama_sampler_accept(e.llg, t);
        text += common_token_to_piece(vocab, t, true);
    }
    // may the output end here?
    const llama_token eog = llama_vocab_eot(vocab) != LLAMA_TOKEN_NULL ? llama_vocab_eot(vocab) : llama_vocab_eos(vocab);
    if (eog != LLAMA_TOKEN_NULL) {
        const bool g = allowed_one(e.gbnf, eog);
        const bool l = allowed_one(e.llg, eog);
        decisions += g ? 'E' : 'e';
        if (g != l && res.status == "OK") {
            res.status = "MISMATCH";
            res.detail = tag + " end of output: GBNF " + (g ? "allows" : "refuses") + ", llguidance " + (l ? "allows" : "refuses") +
                         " after '" + text + "'";
        }
    }
    return decisions;
}

struct grammar_setup {
    std::string              gbnf;
    bool                     lazy = false;
    std::vector<std::string> patterns;
    std::vector<llama_token> trigger_tokens;
    std::vector<llama_token> gbnf_prefill;
    std::string              generation_prompt;
};

// the samplers as common_sampler_init builds them
static bool make_engines(const llama_vocab * vocab, const grammar_setup & g, engines & e, std::string & err) {
    e.llg = llama_sampler_init_llg_from_gbnf(vocab, g.gbnf, g.lazy, g.patterns, g.trigger_tokens,
                                             g.lazy ? std::string() : g.generation_prompt, &err);
    if (!e.llg) {
        return false;
    }
    try {
        std::vector<const char *> pats;
        for (const auto & p : g.patterns) {
            pats.push_back(p.c_str());
        }
        e.gbnf = g.lazy ? llama_sampler_init_grammar_lazy_patterns(vocab, g.gbnf.c_str(), "root", pats.data(), pats.size(),
                                                                    g.trigger_tokens.data(), g.trigger_tokens.size())
                        : llama_sampler_init_grammar(vocab, g.gbnf.c_str(), "root");
        if (e.gbnf && !g.lazy) {
            for (auto t : g.gbnf_prefill) {
                llama_sampler_accept(e.gbnf, t);
            }
        }
    } catch (const std::exception & ex) {
        if (e.gbnf) {
            llama_sampler_free(e.gbnf);
        }
        e.gbnf = nullptr;
        err    = std::string("GBNF: ") + ex.what();
    }
    if (!e.gbnf && err.empty()) {
        err = "GBNF rejected the grammar";
    }
    return true;
}

static void free_engines(engines & e) {
    llama_sampler_free(e.llg);
    llama_sampler_free(e.gbnf);
    e = {};
}

// Replay one sample: full run, then a run that clones both samplers halfway, goes on, restores the
// clones and goes on again; all three must give the same decisions.
static replay_result replay_sample(const llama_vocab * vocab, const grammar_setup & g, const std::string & sample,
                                   const std::string & tag, std::vector<llama_token_data> & cand) {
    replay_result res;
    const auto    tokens = common_tokenize(vocab, sample, false, true);
    res.n_tokens         = tokens.size();

    engines     e;
    std::string err;
    make_engines(vocab, g, e, err);
    const std::string full = replay_from(vocab, e, tokens, 0, true, res, cand, tag);
    res.on_gbnf = std::string(llama_sampler_name(e.llg)) != "llguidance-gbnf";
    free_engines(e);
    if (res.status != "OK") {
        return res;
    }

    // speculative decoding: clone halfway (common_sampler_clone), go on, restore (common_sampler_copy)
    const size_t half = std::min(full.size(), tokens.size()) / 2;
    make_engines(vocab, g, e, err);
    replay_result r2;
    std::string   pre = replay_from(vocab, e, std::vector<llama_token>(tokens.begin(), tokens.begin() + half), 0, false, r2, cand, tag);
    if (!pre.empty() && (pre.back() == 'E' || pre.back() == 'e')) {
        pre.pop_back(); // the end check of the partial run
    }
    llama_sampler * save_llg  = llama_sampler_clone(e.llg);
    llama_sampler * save_gbnf = llama_sampler_clone(e.gbnf);
    const std::string first = replay_from(vocab, e, tokens, half, false, r2, cand, tag);
    llama_sampler_copy(save_llg, e.llg);
    llama_sampler_copy(save_gbnf, e.gbnf);
    const std::string second = replay_from(vocab, e, tokens, half, false, r2, cand, tag);
    llama_sampler_free(save_llg);
    llama_sampler_free(save_gbnf);
    free_engines(e);
    if (r2.status != "OK") {
        return r2;
    }
    if (pre + first != full || first != second) {
        res.status = "MISMATCH";
        res.detail = tag + " clone/restore changed the decisions: " + full + " vs " + pre + "|" + first + "|" + second;
    }
    return res;
}

static std::string read_file(const fs::path & p) {
    std::ifstream     f(p);
    std::stringstream ss;
    ss << f.rdbuf();
    return ss.str();
}

static std::string one_line(std::string s, size_t max = 160) {
    std::replace(s.begin(), s.end(), '\n', ' ');
    return s.size() > max ? s.substr(0, max) + "..." : s;
}

int main(int argc, char ** argv) {
    if (argc < 3) {
        fprintf(stderr, "usage: %s TEMPLATES_DIR VOCAB.gguf [VOCAB.gguf...]\n", argv[0]);
        return 2;
    }
    common_log_set_verbosity_thold(getenv("LLG_CHECK_DEBUG") ? 10 : -1);
    llama_log_set([](ggml_log_level, const char *, void *) {}, nullptr);
    llama_backend_init();

    std::vector<fs::path> templates;
    for (const auto & entry : fs::directory_iterator(argv[1])) {
        if (entry.path().extension() == ".jinja") {
            templates.push_back(entry.path());
        }
    }
    std::sort(templates.begin(), templates.end());
    const char * only = getenv("LLG_CHECK_ONLY");

    int n_pass = 0, n_fail = 0, n_skip = 0, n_on_gbnf = 0;
    std::vector<llama_token_data> cand;

    for (int v = 2; v < argc; v++) {
        auto mparams       = llama_model_default_params();
        mparams.vocab_only = true;
        llama_model * model = llama_model_load_from_file(argv[v], mparams);
        if (!model) {
            fprintf(stderr, "cannot load vocab %s\n", argv[v]);
            return 1;
        }
        const llama_vocab * vocab      = llama_model_get_vocab(model);
        const std::string   vocab_name = fs::path(argv[v]).stem().string();

        for (const auto & path : templates) {
            const std::string name = path.stem().string();
            if (only && name.find(only) == std::string::npos) {
                continue;
            }
            auto skip = [&](const std::string & why) {
                printf("SKIP %-48s %-22s %s\n", name.c_str(), vocab_name.c_str(), one_line(why).c_str());
                n_skip++;
            };

            common_chat_templates_ptr tmpls;
            common_chat_templates_inputs inputs;
            common_chat_msg user;
            user.role           = "user";
            user.content        = "What is the weather in Paris?";
            inputs.messages     = { user };
            inputs.tools        = tool_set();
            inputs.enable_thinking  = false;
            inputs.reasoning_format = COMMON_REASONING_FORMAT_DEEPSEEK;

            common_params_sampling sparams;
            std::string            format;
            std::unique_ptr<common_chat_session> session;
            try {
                tmpls = common_chat_templates_init(model, read_file(path));
                session = std::make_unique<common_chat_session>(tmpls.get(), vocab, inputs);
                common_params_sampling raw;
                session->apply_sampling(raw);
                format = common_chat_format_name(session->format());
                // the server path: preserved tokens, then the triggers (a one-token word becomes a token trigger)
                sparams.grammar           = raw.grammar;
                sparams.grammar_lazy      = raw.grammar_lazy;
                sparams.generation_prompt = raw.generation_prompt;
                sparams.preserved_tokens  = raw.preserved_tokens;
                common_sampling_add_grammar_triggers(sparams, vocab, raw.grammar_triggers);
            } catch (const std::exception & e) {
                skip(std::string("template: ") + e.what());
                continue;
            }
            const std::string & gbnf = common_grammar_value(sparams.grammar);
            if (gbnf.empty()) {
                skip("no tool grammar (" + format + ")");
                continue;
            }

            grammar_setup g;
            g.gbnf              = gbnf;
            g.lazy              = sparams.grammar_lazy;
            g.generation_prompt = sparams.generation_prompt;
            std::string trig;
            for (const auto & t : sparams.grammar_triggers) {
                switch (t.type) {
                    case COMMON_GRAMMAR_TRIGGER_TYPE_WORD:    g.patterns.push_back(regex_escape(t.value)); trig += "W"; break;
                    case COMMON_GRAMMAR_TRIGGER_TYPE_PATTERN: g.patterns.push_back(t.value); trig += "P"; break;
                    case COMMON_GRAMMAR_TRIGGER_TYPE_PATTERN_FULL: {
                        std::string a = "^$";
                        if (!t.value.empty()) {
                            a = (t.value.front() != '^' ? "^" : "") + t.value + (t.value.back() != '$' ? "$" : "");
                        }
                        g.patterns.push_back(a);
                        trig += "F";
                        break;
                    }
                    case COMMON_GRAMMAR_TRIGGER_TYPE_TOKEN: g.trigger_tokens.push_back(t.token); trig += "T"; break;
                }
            }
            if (!g.lazy && !g.generation_prompt.empty()) {
                const auto toks = common_tokenize(vocab, g.generation_prompt, false, true);
                for (size_t i = 0; i < toks.size(); i++) {
                    const std::string piece = common_token_to_piece(vocab, toks[i], true);
                    if (i == 0 && !piece.empty() && std::isspace((unsigned char) piece[0]) && !std::isspace((unsigned char) g.generation_prompt[0])) {
                        continue;
                    }
                    g.gbnf_prefill.push_back(toks[i]);
                }
            }

            // translate and compile
            const auto  t0 = clk::now();
            std::string lark;
            try {
                lark = common_gbnf_to_lark(gbnf, vocab);
            } catch (const std::exception & e) {
                printf("FAIL %-48s %-22s translate: %s\n", name.c_str(), vocab_name.c_str(), one_line(e.what()).c_str());
                n_fail++;
                continue;
            }
            const double translate_ms = std::chrono::duration<double, std::milli>(clk::now() - t0).count();
            if (const char * dump = getenv("LLG_CHECK_DUMP")) {
                std::ofstream(std::string(dump) + "/" + name + "." + vocab_name + ".gbnf") << gbnf;
                std::ofstream(std::string(dump) + "/" + name + "." + vocab_name + ".lark") << lark;
            }
            engines     e;
            std::string err;
            const auto  t1 = clk::now();
            if (!make_engines(vocab, g, e, err)) {
                printf("FAIL %-48s %-22s compile: %s\n", name.c_str(), vocab_name.c_str(), one_line(err, 400).c_str());
                n_fail++;
                continue;
            }
            const double compile_ms = std::chrono::duration<double, std::milli>(clk::now() - t1).count();
            const bool   have_gbnf  = e.gbnf != nullptr;
            free_engines(e);
            if (!have_gbnf) {
                skip("llguidance compiles, GBNF does not: " + err);
                continue;
            }

            // samples: the call is the assistant turn the template renders, after the generation prompt
            std::string call;
            bool        call_parses = false;
            try {
                common_chat_templates_inputs a = inputs;
                const std::string p1 = common_chat_templates_apply(tmpls.get(), a).prompt.str();
                common_chat_msg asst;
                asst.role = "assistant";
                asst.tool_calls.push_back({ "get_weather", R"({"location":"Paris","unit":"celsius"})", "call_1" });
                a.messages.push_back(asst);
                a.add_generation_prompt = false;
                const std::string p2 = common_chat_templates_apply(tmpls.get(), a).prompt.str();
                size_t lcp = 0;
                while (lcp < p1.size() && lcp < p2.size() && p1[lcp] == p2[lcp]) {
                    lcp++;
                }
                if (lcp == p1.size()) {
                    // the model output ends at the first end-of-generation token
                    common_chat_input in;
                    for (auto t : common_tokenize(vocab, p2.substr(lcp), false, true)) {
                        if (llama_vocab_is_eog(vocab, t)) {
                            break;
                        }
                        const std::string piece = common_token_to_piece(vocab, t, true);
                        in.append(piece, t);
                        call += piece;
                    }
                    common_chat_session parse(tmpls.get(), vocab, inputs);
                    std::string parsed;
                    try {
                        const auto & msg = parse.finish(in);
                        call_parses      = !msg.tool_calls.empty();
                        parsed           = msg.to_json_oaicompat().dump();
                    } catch (const std::exception & ex) {
                        call_parses = false;
                        parsed      = ex.what();
                    }
                    if (getenv("LLG_CHECK_SHOW")) {
                        fprintf(stderr, "[%s] generation prompt: %s\n[%s] call: %s\n[%s] parsed: %s\n", name.c_str(),
                                g.generation_prompt.c_str(), name.c_str(), call.c_str(), name.c_str(), parsed.c_str());
                    }
                }
            } catch (const std::exception &) {
                call.clear();
            }

            // the same call after reasoning, as a thinking model writes it (gpt-oss: analysis channel first)
            std::string think_call;
            try {
                common_chat_templates_inputs a = inputs;
                const std::string p1 = common_chat_templates_apply(tmpls.get(), a).prompt.str();
                common_chat_msg asst;
                asst.role              = "assistant";
                asst.reasoning_content = "The user wants the weather in Paris. I will call the tool.";
                asst.tool_calls.push_back({ "get_weather", R"({"location":"Paris","unit":"celsius"})", "call_1" });
                a.messages.push_back(asst);
                a.add_generation_prompt = false;
                const std::string p2 = common_chat_templates_apply(tmpls.get(), a).prompt.str();
                if (p2.compare(0, p1.size(), p1) == 0) {
                    for (auto t : common_tokenize(vocab, p2.substr(p1.size()), false, true)) {
                        if (llama_vocab_is_eog(vocab, t)) {
                            break;
                        }
                        think_call += common_token_to_piece(vocab, t, true);
                    }
                }
            } catch (const std::exception &) {
                think_call.clear();
            }

            std::vector<std::pair<std::string, std::string>> samples = {
                { "free", "The capital of France is Paris." },
            };
            if (getenv("LLG_CHECK_SHOW")) {
                fprintf(stderr, "[%s] think: %s\n", name.c_str(), think_call.c_str());
            }
            if (!think_call.empty() && think_call != call) {
                samples.push_back({ "think", think_call });
            }
            if (!call.empty()) {
                samples.push_back({ "call", call });
                samples.push_back({ "glued", "Let me check.\n" + call });
                samples.push_back({ "glued2", "Sure" + call });
                std::string bad = call;
                const size_t k  = bad.find("location");
                if (k != std::string::npos) {
                    bad.replace(k, 8, "locaton");
                    samples.push_back({ "bad", bad });
                }
            }

            std::string summary;
            std::string failure;
            for (const auto & [tag, text] : samples) {
                const auto r = replay_sample(vocab, g, text, tag, cand);
                summary += " " + tag + "=" + (r.gbnf_reject < 0 ? "acc" : "rej@" + std::to_string(r.gbnf_reject)) + "/" +
                           std::to_string(r.n_tokens);
                if (r.on_gbnf) {
                    summary += "(->gbnf)";
                    n_on_gbnf++;
                }
                if (r.noncanonical) {
                    summary += "(nc" + std::to_string(r.noncanonical) + ")";
                }
                if (r.status != "OK" && failure.empty()) {
                    failure = r.detail;
                }
            }
            if (!call.empty() && !call_parses) {
                summary += " (rendered call does not parse)";
            } else if (call.empty()) {
                summary += " (no rendered call)";
            }

            char head[256];
            snprintf(head, sizeof(head), "%-48s %-22s %-28s lazy=%d trig=%-4s gbnf=%zuB lark=%zuB tr=%.1fms init=%.0fms",
                     name.c_str(), vocab_name.c_str(), format.c_str(), (int) g.lazy, trig.empty() ? "-" : trig.c_str(),
                     gbnf.size(), lark.size(), translate_ms, compile_ms);
            if (failure.empty()) {
                printf("PASS %s%s\n", head, summary.c_str());
                n_pass++;
            } else {
                printf("FAIL %s%s\n     %s\n", head, summary.c_str(), one_line(failure, 400).c_str());
                n_fail++;
            }
            fflush(stdout);
        }
        llama_model_free(model);
    }
    printf("templates: %d pass, %d fail, %d skip; %d replays moved to GBNF\n", n_pass, n_fail, n_skip, n_on_gbnf);
    return n_fail ? 1 : 0;
}
