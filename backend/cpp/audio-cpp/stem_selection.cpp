#include "stem_selection.h"

#include <cstddef>
#include <filesystem>
#include <string>

namespace audiocpp_backend {
namespace {

// The stem every separation family this backend can reach names its lead vocal
// track, and the one a caller who names no stem almost always wants: it is what
// the OpenAI-shaped "isolate the voice" request means. htdemucs (drums, bass,
// other, vocals) and mel_band_roformer (vocals, instrumental) both have it, and
// in htdemucs's case it is NOT the first output, which is the whole reason this
// preference is written down rather than left as "take index 0".
const char *const kPreferredStem = "vocals";

std::string join_names(const std::vector<std::string> &names) {
    std::string out;
    for (const auto &name : names) {
        if (!out.empty()) {
            out += ", ";
        }
        out += name;
    }
    return out;
}

// Whether a model-supplied stem name can be used as one component of a file
// name. Deliberately a whitelist of refusals rather than a sanitiser: silently
// rewriting "vo/cals" to "cals" would make the file the caller receives
// disagree with the name they would have to ask for.
bool name_is_writable(const std::string &name) {
    if (name.empty() || name == "." || name == "..") {
        return false;
    }
    // Both separators, not just the host's. A GGUF is a downloaded file and its
    // strings are not this host's to trust, so a name written on Windows must
    // not become a directory traversal wherever the check happens to run.
    return name.find('/') == std::string::npos &&
           name.find('\\') == std::string::npos;
}

} // namespace

StemChoice select_named_output(const std::vector<std::string> &names,
                               const std::string &requested) {
    StemChoice choice;
    if (names.empty()) {
        // Not an error here. The caller distinguishes "this family produces one
        // unnamed output" from "this family produced nothing", and only it can
        // tell them apart.
        return choice;
    }

    // Every name is checked, not merely the selected one, because every stem is
    // written. A bad name in the fourth output would otherwise be discovered
    // only after three files had already been created.
    for (std::size_t i = 0; i < names.size(); ++i) {
        if (!name_is_writable(names[i])) {
            choice.error = "audio-cpp: this model names an output stem '" +
                           names[i] +
                           "' that cannot be used as a file name; stems: " +
                           join_names(names);
            return choice;
        }
        for (std::size_t seen = 0; seen < i; ++seen) {
            if (names[seen] == names[i]) {
                choice.error =
                    "audio-cpp: this model produces two output stems both named '" +
                    names[i] + "'; one would silently overwrite the other";
                return choice;
            }
        }
    }

    if (!requested.empty()) {
        for (std::size_t i = 0; i < names.size(); ++i) {
            if (names[i] == requested) {
                choice.index = static_cast<int>(i);
                return choice;
            }
        }
        choice.error = "audio-cpp: no stem named '" + requested +
                       "' in this model's output; available stems: " +
                       join_names(names);
        return choice;
    }

    for (std::size_t i = 0; i < names.size(); ++i) {
        if (names[i] == kPreferredStem) {
            choice.index = static_cast<int>(i);
            return choice;
        }
    }
    choice.index = 0;
    return choice;
}

std::string sibling_stem_path(const std::string &dst, const std::string &name) {
    const std::filesystem::path path(dst);
    // The dst extension is reused rather than forced to ".wav" so the siblings
    // look like the file the caller named. write_audio_file writes WAV bytes
    // whatever the extension says, for dst as much as for the siblings, so this
    // keeps the set consistent instead of making the siblings honest about a
    // format dst is already lying about.
    const std::string extension =
        path.has_extension() ? path.extension().string() : std::string(".wav");
    return (path.parent_path() / (path.stem().string() + "." + name + extension))
        .string();
}

} // namespace audiocpp_backend
