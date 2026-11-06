#pragma once
#include "capability_routing.h"
#include "backend.pb.h"
#include <string>
namespace audiocpp_backend {
enum class MusicReadiness { Unknown, Unsupported, GraphOnly, CompleteOffline };
struct MusicAdmissionFacts {
    std::string loaded_identity;
    Capabilities capabilities;
    std::string pinned_task;
    bool inline_abc = false;
    MusicReadiness readiness = MusicReadiness::Unknown;
};
// Pure eligibility only. Production never synthesizes positive readiness facts.
bool music_eligible(const MusicAdmissionFacts &, Rpc, const std::string &requested_identity, bool has_abc);
std::string validate_music(const backend::MusicTranscriptionRequest &);
std::string validate_music(const backend::MusicGenerationRequest &);
} // namespace audiocpp_backend
