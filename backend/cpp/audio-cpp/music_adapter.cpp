#include "music_adapter.h"
#include <cmath>
#include <cctype>
#include <filesystem>
namespace audiocpp_backend {
namespace {
bool text(const std::string&s,size_t cap,bool nonblank){
 if(s.size()>cap||s.find('\0')!=std::string::npos)return false;
 bool content=false;size_t i=0;
 while(i<s.size()){
  unsigned char c=s[i++];if(!std::isspace(c))content=true;
  if(c<128)continue;
  unsigned count;unsigned value,min;
  if(c>=0xc2&&c<=0xdf){count=1;value=c&31;min=128;}
  else if(c>=0xe0&&c<=0xef){count=2;value=c&15;min=2048;}
  else if(c>=0xf0&&c<=0xf4){count=3;value=c&7;min=65536;}else return false;
  if(i+count>s.size())return false;
  while(count--){unsigned char next=s[i++];if((next&0xc0)!=0x80)return false;value=(value<<6)|(next&63);}
  if(value<min||value>0x10ffff||(value>=0xd800&&value<=0xdfff))return false;
 }
 return !nonblank||content;
}
bool path(const std::string&s){return text(s,4096,true)&&std::filesystem::path(s).is_absolute()&&std::filesystem::path(s).lexically_normal().string()==s;}
std::string envelope(const std::string&id,const std::string&model,const std::string&p){return text(id,128,true)&&text(model,128,true)&&path(p)?"":"invalid typed music envelope";}
}
bool music_eligible(const MusicAdmissionFacts&f,Rpc rpc,const std::string&requested,bool has_abc){
 if(requested.empty()||f.loaded_identity.empty()||requested!=f.loaded_identity||f.readiness!=MusicReadiness::CompleteOffline)return false;
 const bool transcribe=rpc==Rpc::MusicTranscription;
 if(!transcribe&&rpc!=Rpc::MusicGeneration)return false;
 if(f.capabilities.family!=(transcribe?"sheetsage2":"yue2"))return false;
 Task pinned;if(!parse_task_name(f.pinned_task,pinned)||pinned!=(transcribe?Task::Midi:Task::AudioGeneration))return false;
 if(has_abc&&!f.inline_abc)return false;
 RequestShape shape;shape.pinned_task=f.pinned_task;auto route=resolve_route(rpc,shape,f.capabilities);
 return route.ok&&route.mode==Mode::Offline&&route.task==pinned;
}
std::string validate_music(const backend::MusicTranscriptionRequest&r){return envelope(r.request_id(),r.model_identity(),r.trusted_source());}
std::string validate_music(const backend::MusicGenerationRequest&r){
 auto error=envelope(r.request_id(),r.model_identity(),r.trusted_output_destination());if(!error.empty())return error;
 const auto&p=r.params();
 if(!text(r.style(),4<<10,true)||!text(r.lyrics(),16<<10,false)||!r.has_params()||p.cot()<backend::MUSIC_COT_OFF||p.cot()>backend::MUSIC_COT_FULL||p.seed()>2147483647u||!std::isfinite(p.guidance_scale())||p.guidance_scale()<0||p.guidance_scale()>20||p.num_inference_steps()<1||p.num_inference_steps()>100)return "invalid typed music fields or materialized parameters";
 if(r.has_abc()){
  if(!text(r.abc(),256<<10,true)||p.cot()==backend::MUSIC_COT_OFF)return "invalid provided music ABC";
  // Full notation parsing remains an executor gate. No production call runs here.
  size_t start=0;while(start<r.abc().size()){auto end=r.abc().find('\n',start);auto line=r.abc().substr(start,end==std::string::npos?end:end-start);auto first=line.find_first_not_of(" \t\r");if(first!=std::string::npos){line.erase(0,first);if(line.rfind("%%",0)==0||line.rfind("I:",0)==0||line.find("[I:")!=std::string::npos)return "music ABC directives are unsupported";}if(end==std::string::npos)break;start=end+1;}
 }
 return "";
}
} // namespace audiocpp_backend
