#include "generation_request.h"

#include <filesystem>
#include <string>
#include <system_error>
#include <utility>

namespace audiocpp_backend {
namespace {

const char *bool_option(bool value) { return value ? "true" : "false"; }

} // namespace

bool voice_is_reference_file(const std::string &voice) {
    if (voice.empty()) {
        return false;
    }
    std::error_code ec;
    return std::filesystem::is_regular_file(std::filesystem::path(voice), ec);
}

RequestShape build_tts_shape(const backend::TTSRequest &request) {
    RequestShape shape;
    shape.has_voice_reference = voice_is_reference_file(request.voice()) ||
                                request.params().find("multi_reference_cond") != request.params().end();
    // !empty() as well as has_instructions(), and it must match the guard in
    // build_tts_request: a request whose instructions are an empty string
    // carries no style condition, so telling routing to prefer VoiceDesign for
    // it would route to a task with nothing to design from.
    shape.has_instructions =
        request.has_instructions() && !request.instructions().empty();
    return shape;
}

engine::runtime::TaskRequest
build_tts_request(const backend::TTSRequest &request,
                  std::optional<engine::runtime::AudioBuffer> reference_audio) {
    engine::runtime::TaskRequest task;

    // The Transcript, not an option, is where every TTS family reads its
    // language: chatterbox normalises request.text_input->language into its
    // voice-clone config, qwen3_tts reads it as out.language, ace_step turns it
    // into vocal_language. Upstream's own HTTP server does the same and sets no
    // language option at all (app/server/runtime.cpp build_speech_request).
    engine::runtime::Transcript transcript;
    transcript.text = request.text();
    if (request.has_language()) {
        transcript.language = request.language();
    }
    task.text_input = std::move(transcript);

    engine::runtime::VoiceCondition condition;
    bool condition_used = false;

    if (reference_audio.has_value()) {
        // A clip: VoiceReference::audio, at the file's own rate and channel
        // count. See kVoiceReferenceSampleRate in grpc-server.cpp for why it is
        // not folded first.
        engine::runtime::VoiceReference reference;
        reference.audio = std::move(*reference_audio);
        condition.speaker = std::move(reference);
        condition_used = true;
    } else if (!request.voice().empty()) {
        // A named preset. cached_voice_id is the channel that actually lands:
        // supertonic (options.voice), pocket_tts (voice_config.preset_name),
        // voxcpm2, vibevoice, fish_audio (its saved-reference lookup) and
        // qwen3_tts CustomVoice all read request.voice->speaker->cached_voice_id,
        // and upstream's own server puts a non-preset `voice` body field in
        // exactly this slot (app/server/runtime.cpp build_speech_request).
        engine::runtime::VoiceReference reference;
        reference.cached_voice_id = request.voice();
        condition.speaker = std::move(reference);
        condition_used = true;
        // Forward-tolerant alias only. A bare "voice" REQUEST OPTION is read by
        // no family in the pinned upstream: grepping find_option for it returns
        // nothing. It is sent so a family adopting the name later works with no
        // change here, not because it does anything today.
        task.options["voice"] = request.voice();
    }

    if (request.has_instructions() && !request.instructions().empty()) {
        // "instruct" is the key upstream itself maps the OpenAI `instructions`
        // body field onto (app/server/runtime.cpp: request.options["instruct"]
        // = value->as_string()), and it is read: qwen3_tts VoiceDesign and
        // CustomVoice both take find_option(options, {"instruct"}) first, and
        // omnivoice reads it in resolve_instruct.
        task.options["instruct"] = request.instructions();
        // "caption" is irodori_tts's name for the same thing, read in its
        // make_request and documented as the voice-design caption for the 600M
        // VoiceDesign model (docs/tts.md). Without this, that family's voice
        // design cannot be driven from this RPC at all.
        task.options["caption"] = request.instructions();
        // The proto field's own name, forwarded for the same forward-tolerant
        // reason as "voice" above and with the same honest accounting: NO family
        // in the pinned upstream reads a request option called "instructions".
        task.options["instructions"] = request.instructions();

        engine::runtime::StyleCondition style;
        // "instruct", not "instructions". This tag IS read, and only under that
        // spelling: omnivoice and qwen3_tts both fall back to
        // request.voice->style->tags.find("instruct") when the option is absent.
        // Spelling it "instructions" here would have made the whole
        // StyleCondition dead weight.
        style.tags["instruct"] = request.instructions();
        // !empty(), matching the option emission below, and load-bearing rather
        // than tidiness. core/backend/tts.go's newTTSRequest sets
        // `Language: &language` UNCONDITIONALLY, so has_language() is true on
        // every request LocalAI sends and carries "" whenever the caller named
        // no language. An engaged-but-empty style language is WORSE than an
        // absent one: supertonic reads text_input->language behind its own
        // !empty() guard and then OVERRIDES it from style->language with no
        // guard at all (supertonic/session.cpp), so "" would replace its "en"
        // default and tokenizer_text.cpp would throw
        // "invalid Supertonic language: " on every request that set
        // instructions and no language.
        if (request.has_language() && !request.language().empty()) {
            style.language = request.language();
        }
        condition.style = std::move(style);
        condition_used = true;
    }

    if (condition_used) {
        task.voice = std::move(condition);
    }

    if (request.has_language() && !request.language().empty()) {
        // Forward-tolerant alias, exactly as in build_transcription_request. The
        // families that read a "language" request option are the ASR ones
        // (nemotron_asr, hviske_asr, vibevoice_asr, higgs_audio_stt), none of
        // which this RPC can route to; pocket_tts reads one but from its
        // ModelLoadRequest at load time, not from here. The Transcript above is
        // what actually carries the language to a TTS family.
        task.options["language"] = request.language();
    }

    // LAST, so an explicit params entry wins over anything derived above. That
    // matters for "caption": a caller who sets params[caption] has named the
    // exact string they want, and it must not be overwritten by `instructions`.
    for (const auto &param : request.params()) {
        task.options[param.first] = param.second;
    }
    return task;
}

engine::runtime::TaskRequest
build_sound_generation_request(const backend::SoundGenerationRequest &request,
                               std::optional<engine::runtime::AudioBuffer> source_audio) {
    engine::runtime::TaskRequest task;

    engine::runtime::Transcript transcript;
    transcript.text = request.text();
    if (request.has_language()) {
        transcript.language = request.language();
    }
    task.text_input = std::move(transcript);

    // src is the input clip for the editing routes. ace_step's repaint, cover
    // and edit routes need it; stable_audio uses it as init_audio or
    // inpaint_audio; heartmula refuses it outright.
    if (source_audio.has_value()) {
        task.audio_input = std::move(*source_audio);
    }

    // WHAT LANDS AND WHAT DOES NOT. Three families advertise AudioGeneration in
    // the pinned upstream: ace_step, heartmula and stable_audio. Every key below
    // was grepped against find_option/parse_*_option in src/ and include/ rather
    // than assumed, because a key nobody reads is not a feature and shipping one
    // while implying it works is the mistake this comment exists to prevent.
    //
    // Unknown REQUEST options cannot turn a valid request into an error:
    // families look theirs up by name and ignore the rest, and the unknown-key
    // refusals upstream does have are on SESSION options, which arrive at load
    // time. So a forward-tolerant alias is free; it is just not a feature.

    if (request.has_duration()) {
        // duration_seconds is the key that works, and it works everywhere:
        // ace_step (request_parser.cpp), heartmula (session.cpp, which also
        // refuses a non-positive value) and stable_audio (request.cpp) all read
        // it. This is the SoundGeneration analogue of Task 9's return_timestamps.
        task.options["duration_seconds"] = std::to_string(request.duration());
        // The proto field's own name. Read by exactly one family, omnivoice, and
        // omnivoice advertises Tts rather than AudioGeneration, so this RPC can
        // never route to it: DEAD here, kept only as a forward-tolerant alias.
        task.options["duration"] = std::to_string(request.duration());
    }
    if (request.has_temperature()) {
        // Read by heartmula. ace_step's sampling temperature is a different,
        // narrower knob it calls lm_temperature (it drives the caption/thinking
        // LM, not the audio diffusion), so this is deliberately NOT mapped onto
        // it; a caller who wants it sets it through the request options that
        // reach ace_step by name. stable_audio has no temperature at all.
        task.options["temperature"] = std::to_string(request.temperature());
    }
    if (request.has_sample()) {
        // do_sample is read widely upstream, but only by TTS and ASR families
        // (chatterbox, index_tts2, miotts, moss, qwen3_tts, vibevoice,
        // hviske_asr, voxtral_realtime). NO AudioGeneration family reads it, so
        // it is dead on this route.
        task.options["do_sample"] = bool_option(request.sample());
    }
    if (request.has_src_divisor()) {
        // Read by nobody, anywhere in the pinned upstream. Forwarded because the
        // proto documents it as part of this request and a family adopting it
        // then works unchanged.
        task.options["src_divisor"] = std::to_string(request.src_divisor());
    }
    if (request.has_think()) {
        // "thinking" is the key ace_step actually reads (request_parser.cpp),
        // which is why it is sent alongside the proto's own "think". "think" on
        // its own is read by nobody.
        task.options["thinking"] = bool_option(request.think());
        task.options["think"] = bool_option(request.think());
    }
    if (request.has_caption()) {
        // Read only by irodori_tts, which advertises Tts/VoiceCloning/VoiceDesign
        // and not AudioGeneration, so it is unreachable from this RPC: DEAD here.
        task.options["caption"] = request.caption();
    }
    if (request.has_lyrics()) {
        // Read by ace_step and heartmula.
        task.options["lyrics"] = request.lyrics();
    }
    if (request.has_bpm()) {
        // Read by ace_step.
        task.options["bpm"] = std::to_string(request.bpm());
    }
    if (request.has_keyscale()) {
        // Read by ace_step.
        task.options["keyscale"] = request.keyscale();
    }
    if (request.has_timesignature()) {
        // Read by ace_step.
        task.options["timesignature"] = request.timesignature();
    }
    if (request.has_instrumental()) {
        // Read by nobody: "instrumental" appears in the pinned upstream only as
        // a roformer STEM NAME, never as a request option. A forward-tolerant
        // alias and nothing more.
        task.options["instrumental"] = bool_option(request.instrumental());
    }
    if (request.has_language() && !request.language().empty()) {
        // Alias again: the Transcript above is what ace_step reads as
        // vocal_language. No AudioGeneration family reads a "language" option.
        task.options["language"] = request.language();
    }
    return task;
}

bool apply_transform_text_input(engine::runtime::TaskRequest &task) {
    // Canonical first, alias second, and an empty value falls through to the
    // next candidate rather than ending the search: a caller who sent
    // target_text="" and text="the real one" meant the second one.
    static const char *const kTextKeys[] = {"target_text", "text"};

    std::string text;
    for (const char *key : kTextKeys) {
        const auto found = task.options.find(key);
        if (found != task.options.end() && !found->second.empty()) {
            text = found->second;
            break;
        }
    }
    if (text.empty()) {
        return false;
    }

    engine::runtime::Transcript transcript;
    transcript.text = std::move(text);
    // Inside the has-text branch on purpose. See the header: a language on its
    // own conditions nothing and must not manufacture a text_input.
    const auto language = task.options.find("language");
    if (language != task.options.end()) {
        transcript.language = language->second;
    }
    task.text_input = std::move(transcript);
    return true;
}

} // namespace audiocpp_backend
