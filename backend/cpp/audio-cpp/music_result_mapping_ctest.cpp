// Synthetic real upstream structs, not model output fixtures or inference.
#include "music_result_mapping.h"
#include <cstdio>
#include <limits>
#include <functional>
using namespace audiocpp_backend;
namespace rt = engine::runtime;
static int failures = 0, checks = 0;
static void check(bool ok, const char *name) {
    ++checks;
    if (!ok) { ++failures; std::fprintf(stderr, "FAIL: %s\n", name); }
}
static const std::string abc = "X:1\r\nT:Synthetic café\nK:C\nC D E F|\n";
static rt::VoiceArtifact score(bool generated = false, const std::string &text = abc) {
    auto a = rt::make_text_artifact(rt::ArtifactKind::Custom, "score", text,
        {{"mime", "text/vnd.abc"}, {"format", "abc"}, {"extension", "abc"}});
    if (generated) { a.meta["source"] = "generated"; a.meta["truncated"] = "false"; }
    return a;
}
static rt::TaskResult transcription(const std::string &text = abc) {
    rt::TaskResult r;
    r.artifact_output = score(false, text);
    r.text_output = rt::Transcript{text, "abc"};
    r.output_artifacts.push_back(rt::make_text_artifact(rt::ArtifactKind::Custom,
        "events", "{}", {{"format", "sheetsage2-events-json"}}));
    return r;
}
static rt::TaskResult generation() {
    rt::TaskResult r;
    r.audio_output = rt::AudioBuffer{24000, 2, {0.0f, 0.1f, -0.1f, 0.0f}};
    return r;
}
static void bad_transcription(const std::function<void(rt::TaskResult &)> &mutate) {
    auto r = transcription(); mutate(r);
    auto m = map_music_transcription(r);
    check(!m.ok() && !m.abc, "invalid transcription fails without leaking ABC");
}
static void bad_generation(const std::function<void(rt::TaskResult &)> &mutate) {
    auto r = generation(); r.output_artifacts.push_back(score(true)); mutate(r);
    auto m = map_music_generation(r);
    check(!m.ok() && !m.abc, "invalid generation fails without partial ABC");
}
int main() {
    auto t = transcription(); auto m = map_music_transcription(t);
    check(m.ok() && m.abc == abc, "exact transcription bytes including CRLF and UTF8");
    bad_transcription([](auto &r) { r.artifact_output.reset(); });
    bad_transcription([](auto &r) { r.text_output.reset(); });
    bad_transcription([](auto &r) { r.text_output->text += " "; });
    bad_transcription([](auto &r) { r.text_output->language = "en"; });
    bad_transcription([](auto &r) { r.artifact_output->id = "other"; });
    bad_transcription([](auto &r) { r.artifact_output->kind = rt::ArtifactKind::Midi; });
    bad_transcription([](auto &r) { r.output_artifacts.push_back(score()); });
    for (const char *key : {"mime", "format", "extension"}) {
        bad_transcription([key](auto &r) { r.artifact_output->meta.erase(key); });
        bad_transcription([key](auto &r) { r.artifact_output->meta[key] = "/private/native/path"; });
    }
    bad_transcription([](auto &r) { r.artifact_output->meta["source"] = "generated"; });
    bad_transcription([](auto &r) { r.artifact_output->meta["truncated"] = "true"; });
    for (const auto &text : std::vector<std::string>{"", " \n\t\r", std::string("x\0y", 3),
            "\xc0\xaf", "\xed\xa0\x80", "\xf4\x90\x80\x80", "\xe2\x82", "\x80",
            std::string(kMusicAbcMaxBytes + 1, 'x')}) {
        auto r = transcription(text); auto out = map_music_transcription(r);
        check(!out.ok() && !out.abc, "bad text bounded/UTF8/NUL/blank");
    }
    bad_transcription([](auto &r) { r.output_artifacts.push_back(score(false, "different")); });
    bad_transcription([](auto &r) { auto a = score(); a.id = "wrong"; r.output_artifacts.push_back(a); });
    auto boundary = transcription(std::string(kMusicAbcMaxBytes, 'x'));
    check(map_music_transcription(boundary).ok(), "inclusive byte bound (not syntax validation)");
    auto g = generation();
    check(map_music_generation(g).ok() && !map_music_generation(g).abc, "audio without generated ABC accepted");
    g.output_artifacts.push_back(score(true));
    check(map_music_generation(g).abc == abc && map_music_generation(g).ok(), "exact generated ABC");
    for (const char *key : {"mime", "format", "extension", "source", "truncated"}) {
        bad_generation([key](auto &r) { r.output_artifacts[0].meta.erase(key); });
        bad_generation([key](auto &r) { r.output_artifacts[0].meta[key] = "wrong"; });
    }
    bad_generation([](auto &r) { r.output_artifacts[0].meta["truncated"] = "true"; });
    bad_generation([](auto &r) { r.output_artifacts[0].kind = rt::ArtifactKind::Midi; });
    bad_generation([](auto &r) { r.artifact_output = score(true); });
    bad_generation([](auto &r) { r.output_artifacts.push_back(score(true)); });
    bad_generation([](auto &r) { r.text_output = rt::Transcript{abc, "abc"}; });
    bad_generation([](auto &r) { r.output_artifacts[0] = score(true, "\xc0\xaf"); });
    bad_generation([](auto &r) { r.audio_output.reset(); });
    bad_generation([](auto &r) { r.audio_output->samples.clear(); });
    bad_generation([](auto &r) { r.audio_output->sample_rate = 0; });
    bad_generation([](auto &r) { r.audio_output->channels = 0; });
    bad_generation([](auto &r) { r.audio_output->channels = 3; });
    bad_generation([](auto &r) { r.audio_output->samples.pop_back(); });
    bad_generation([](auto &r) { r.audio_output->samples[0] = std::numeric_limits<float>::infinity(); });
    bad_generation([](auto &r) { r.audio_output->samples[0] = std::numeric_limits<float>::quiet_NaN(); });
    bad_generation([](auto &r) { r.audio_output->sample_rate = 1; r.audio_output->samples.resize(602); });
    bad_generation([](auto &r) { r.output_artifacts[0].id = "wrong"; });
    bad_generation([](auto &r) { r.output_artifacts.push_back(score(true, "different")); });
    bad_generation([](auto &r) { r.output_artifacts[0] = score(true, std::string(kMusicAbcMaxBytes + 1, 'x')); });
    bad_generation([](auto &r) { r.output_artifacts[0] = score(true, " \n\t"); });
    bad_generation([](auto &r) { r.named_audio_outputs.push_back(rt::NamedAudioBuffer{}); });
    auto duration_boundary = generation();
    duration_boundary.audio_output->sample_rate = 1;
    duration_boundary.audio_output->samples.resize(600);
    check(map_music_generation(duration_boundary).ok(), "inclusive 300 second in-memory duration");
    auto max_score = generation();
    max_score.output_artifacts.push_back(score(true, std::string(kMusicAbcMaxBytes, 'x')));
    check(map_music_generation(max_score).ok(), "generated ABC inclusive bound");
    auto no_score = generation();
    no_score.output_artifacts.push_back(rt::make_text_artifact(rt::ArtifactKind::Custom, "semantic", "{}"));
    check(map_music_generation(no_score).ok() && !map_music_generation(no_score).abc,
          "non-score artifacts ignored rather than exposed");
    auto semantic = generation(); semantic.audio_output.reset();
    semantic.output_artifacts.push_back(rt::make_text_artifact(rt::ArtifactKind::Custom, "semantic", "{}"));
    check(!map_music_generation(semantic).ok(), "semantic-only is not audio success");
    std::fprintf(stderr, "%d checks, %d failures\n", checks, failures);
    return failures ? 1 : 0;
}
