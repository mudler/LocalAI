#include "music_adapter.h"
#include <iostream>
#include <stdexcept>
#include <limits>
using namespace audiocpp_backend;
void require(bool value,const char*message){if(!value)throw std::runtime_error(message);}
int main(){try{
 MusicAdmissionFacts f;f.loaded_identity="loaded";f.capabilities={"yue2",{{Task::AudioGeneration,{Mode::Offline}}}};f.pinned_task="gen";f.inline_abc=true;f.readiness=MusicReadiness::CompleteOffline;
 require(music_eligible(f,Rpc::MusicGeneration,"loaded",true),"synthetic complete facts eligible");
 for(auto ready:{MusicReadiness::Unknown,MusicReadiness::Unsupported,MusicReadiness::GraphOnly}){f.readiness=ready;require(!music_eligible(f,Rpc::MusicGeneration,"loaded",false),"GraphOnly/unknown/unsupported never eligible");}
 f.readiness=MusicReadiness::CompleteOffline;require(!music_eligible(f,Rpc::MusicGeneration,"",false),"empty identity denied");require(!music_eligible(f,Rpc::MusicGeneration,"stale",false),"stale identity denied");f.loaded_identity="";require(!music_eligible(f,Rpc::MusicGeneration,"loaded",false),"empty loaded identity denied");f.loaded_identity="loaded";f.inline_abc=false;require(!music_eligible(f,Rpc::MusicGeneration,"loaded",true),"unknown inline support denied");f.inline_abc=true;f.pinned_task="midi";require(!music_eligible(f,Rpc::MusicGeneration,"loaded",false),"wrong pin denied");f.pinned_task="gen";f.capabilities.family="other";require(!music_eligible(f,Rpc::MusicGeneration,"loaded",false),"wrong family denied");
 f.capabilities={"sheetsage2",{{Task::Midi,{Mode::Offline}}}};f.pinned_task="midi";require(music_eligible(f,Rpc::MusicTranscription,"loaded",false),"synthetic transcription facts eligible");f.capabilities.tasks.clear();require(!music_eligible(f,Rpc::MusicTranscription,"loaded",false),"missing real task denied");
 backend::MusicGenerationRequest r;r.set_request_id("request");r.set_model_identity("loaded");r.set_style("jazz");r.set_trusted_output_destination("/private/result.wav");auto*p=r.mutable_params();p->set_cot(backend::MUSIC_COT_FULL);p->set_seed(2147483647);p->set_guidance_scale(0);p->set_num_inference_steps(100);require(validate_music(r).empty(),"explicit ranges guidance zero accepted");
 r.set_abc("");std::string wire=r.SerializeAsString();backend::MusicGenerationRequest decoded;require(decoded.ParseFromString(wire)&&decoded.has_abc(),"wire present empty retained");require(!validate_music(decoded).empty(),"present empty ABC invalid");r.set_abc("X:1\nK:C\nC D E|\n");require(validate_music(r).empty(),"safe ABC bytes accepted");r.set_abc("%%include /etc/passwd");require(!validate_music(r).empty(),"directive denied before open");r.clear_abc();p->set_cot(backend::MUSIC_COT_UNSPECIFIED);require(!validate_music(r).empty(),"unset cot denied");p->set_cot(backend::MUSIC_COT_FULL);p->set_guidance_scale(std::numeric_limits<double>::infinity());require(!validate_music(r).empty(),"nonfinite guidance denied");
 std::cout<<"PASS typed music admission and wire validation\n";return 0;
 }catch(const std::exception&e){std::cerr<<"FAIL: "<<e.what()<<"\n";return 1;}}
