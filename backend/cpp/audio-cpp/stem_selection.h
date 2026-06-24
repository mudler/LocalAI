#pragma once

// Decides which of a separation model's named stems the AudioTransform response
// carries in `dst`, and names the sibling files every other stem is written to.
//
// It exists because AudioTransformResult carries ONE dst while htdemucs and
// mel_band_roformer produce several named outputs from a single run. Running
// once per stem would cost four full inferences for a four stem model, so the
// handler runs once, writes every stem beside dst, and puts the selected one in
// dst itself.
//
// Standard library only, so backend/cpp/run-unit-tests.sh compiles and runs its
// test without an audio.cpp checkout. grpc-server.cpp flattens the engine's
// NamedAudioBuffer list into the plain name vector taken here; nothing in this
// unit knows about engine::runtime.

#include <string>
#include <vector>

namespace audiocpp_backend {

struct StemChoice {
    // Index into the `names` vector. -1 means nothing was chosen, which happens
    // for an empty list (the family produced a single unnamed output) and for
    // every refusal.
    //
    // THE CONTRACT THE CALLER INDEXES ON: when `error` is empty and `names` was
    // not, this is always a valid index into `names`. It is never -1 in that
    // case, so a caller that checks `error` first can index without a further
    // guard, and a caller that does not check `error` first would index with a
    // negative value. Check the error.
    int index = -1;
    // Non-empty when the request must be refused, and suitable verbatim as an
    // INVALID_ARGUMENT message. Two things land here: the caller named a stem
    // this model does not produce, and the model named stems that cannot both
    // be written (an unusable file name, or two stems sharing one).
    std::string error;
};

// Picks the stem that goes to dst. Preference order: an explicit `requested`,
// then "vocals", then the first output.
//
// An explicit but unknown `requested` is an ERROR rather than a fallback. A
// caller who asks for "drums" and silently receives "vocals" gets a 200 and a
// wrong file, which is the failure mode nobody can see; the message therefore
// lists the stem names this model really has.
//
// The names are also validated, because they come from the MODEL (htdemucs
// reads them from the GGUF's config.sources) and each one becomes a component
// of a file path this backend writes. A name carrying a path separator would
// write outside the caller's output directory, and two stems sharing a name
// would silently overwrite each other. Both are refused before anything is
// written, which is also why selection has to happen before the first write
// rather than after the loop: a refused request must leave no files behind.
StemChoice select_named_output(const std::vector<std::string> &names,
                               const std::string &requested);

// "/generated/transform-1.wav" + "drums" -> "/generated/transform-1.drums.wav".
// A dst with no extension gets ".wav", since that is what write_audio_file
// produces whatever the caller called the file.
std::string sibling_stem_path(const std::string &dst, const std::string &name);

} // namespace audiocpp_backend
