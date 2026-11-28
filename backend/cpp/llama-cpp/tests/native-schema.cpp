// SPDX-License-Identifier: MIT
#include "chat.h"
#include "native-json-test.h"
#include "llama-grammar.h"
#include <fstream>
#include <sstream>
#include <iostream>
#include <stdexcept>
#include <chrono>
#include <filesystem>
std::string read(const char *p){std::ifstream f(p); if(!f)throw std::runtime_error(p);std::stringstream s;s<<f.rdbuf();return s.str();}
bool match(const std::string &g,const std::string &s){
 auto *p=llama_grammar_init_impl(nullptr,g.c_str(),"root",false,nullptr,0,nullptr,0);
 if(!p)throw std::runtime_error("grammar init failed");
 bool ok=true; for(size_t i=0;i<s.size();){
 unsigned char lead=s[i++];uint32_t c=lead;int n=0;
 if(lead>=0xf0){c=lead&7;n=3;}else if(lead>=0xe0){c=lead&15;n=2;}else if(lead>=0xc0){c=lead&31;n=1;}
 while(n--){if(i>=s.size())throw std::runtime_error("invalid UTF-8 fixture");c=(c<<6)|(s[i++]&63);}
 llama_grammar_accept(p,c);if(llama_grammar_get_stacks(p).empty()){ok=false;break;}}
 bool complete=false;if(ok)for(auto &stack:llama_grammar_get_stacks(p))if(stack.empty())complete=true;
 llama_grammar_free_impl(p);return ok&&complete;
}
struct Fixture {
 std::string name,args;
 bool grammar_expected,parser_expected;
 common_json expected_json;
};
int main(int argc,char **argv){try{
 if(argc!=3)throw std::runtime_error("usage: native-schema schema.json output-dir");
 std::filesystem::create_directories(argv[2]);
 auto schema=common_json::parse(read(argv[1]));
 if(schema["properties"]["keyscale"]["enum"].size()!=504 || schema["required"]!=common_json::array({"caption","lyrics"}))throw std::runtime_error("schema invariant failed");
 // Deliberately synthetic; only public format markers, never an installed/private prompt.
 std::string tmpl="List of tools: [\n{% for tool in tools %}{{ tool | tojson }}\n{% endfor %}]\n{% for message in messages %}<|im_start|>{{ message.role }}\n{{ message.content }}<|im_end|>\n{% endfor %}{% if add_generation_prompt %}<|im_start|>assistant\n{% endif %}";
 std::ofstream(std::string(argv[2])+"/synthetic-template.jinja")<<tmpl;
 auto templates=common_chat_templates_init(nullptr,tmpl);
 std::vector<Fixture> fixtures={
 {"valid","caption='piano', lyrics=''",true,true,{{"caption","piano"},{"lyrics",""}}},
 {"caption_only","caption='piano'",false,false,common_json()},
 {"lyrics_only","lyrics=''",false,false,common_json()},
 {"bpm_only","bpm=120",false,false,common_json()},
 {"empty_arguments","",false,false,common_json()},
 {"valid_enum","caption='piano', keyscale='C major', lyrics=''",true,true,{{"caption","piano"},{"keyscale","C major"},{"lyrics",""}}},
 {"invalid_enum","caption='piano', keyscale='H major', lyrics=''",false,false,common_json()},
 {"caption_wrong_type","caption=123, lyrics=''",false,false,common_json()},
 {"lyrics_wrong_type","caption='piano', lyrics=null",false,false,common_json()},
 {"bpm_wrong_type","bpm='fast', caption='piano', lyrics=''",false,false,common_json()},
 {"bpm_boolean","bpm=true, caption='piano', lyrics=''",false,false,common_json()},
 {"duplicate_caption","caption='first', caption='second', lyrics=''",false,false,common_json()},
 {"unknown_arg","caption='piano', lyrics='', unknown=1",false,false,common_json()},
 {"bpm_lower","bpm=0, caption='piano', lyrics=''",false,false,common_json()},
 {"bpm_upper","bpm=401, caption='piano', lyrics=''",false,false,common_json()},
 {"bpm_fraction","bpm=120.5, caption='piano', lyrics=''",false,false,common_json()},
 {"duration_upper","caption='piano', duration_seconds=601, lyrics=''",false,false,common_json()},
 {"caption_empty","caption='', lyrics=''",false,false,common_json()},
 {"caption_too_long","caption='"+std::string(501,'x')+"', lyrics=''",false,false,common_json()},
 {"lyrics_too_long","caption='piano', lyrics='"+std::string(4001,'x')+"'",false,false,common_json()},
 {"valid_bpm_lower","bpm=1, caption='piano', lyrics=''",true,true,{{"bpm",1},{"caption","piano"},{"lyrics",""}}},
 {"valid_bpm_upper","bpm=400, caption='piano', lyrics=''",true,true,{{"bpm",400},{"caption","piano"},{"lyrics",""}}},
 {"valid_duration_fraction","caption='piano', duration_seconds=1.25, lyrics=''",true,true,{{"caption","piano"},{"duration_seconds",1.25},{"lyrics",""}}},
 {"valid_duration_upper","caption='piano', duration_seconds=600.00, lyrics=''",true,true,{{"caption","piano"},{"duration_seconds",600},{"lyrics",""}}},
 {"duration_upper_fraction","caption='piano', duration_seconds=600.01, lyrics=''",false,false,common_json()},
 {"duration_lower","caption='piano', duration_seconds=0.99, lyrics=''",false,false,common_json()},
 {"duration_exponent","caption='piano', duration_seconds=1e3, lyrics=''",false,false,common_json()},
 {"leading_zero_integer","bpm=0120, caption='piano', lyrics=''",false,false,common_json()},
 {"valid_meter","caption='piano', lyrics='', timesignature='04/32'",true,true,{{"caption","piano"},{"lyrics",""},{"timesignature","04/32"}}},
 {"invalid_meter_zero","caption='piano', lyrics='', timesignature='0/4'",false,false,common_json()},
 {"invalid_meter_high","caption='piano', lyrics='', timesignature='33/4'",false,false,common_json()},
 {"valid_caption_bound","caption='"+std::string(500,'x')+"', lyrics=''",true,true,{{"caption",std::string(500,'x')},{"lyrics",""}}},
 {"valid_lyrics_bound","caption='piano', lyrics='"+std::string(4000,'x')+"'",true,true,{{"caption","piano"},{"lyrics",std::string(4000,'x')}}},
 {"valid_unicode","caption='音楽🎵', lyrics=''",true,true,{{"caption","音楽🎵"},{"lyrics",""}}},
 {"valid_escapes","caption='a\\n\\u0041\\uD83C\\uDFB5', lyrics=''",true,true,{{"caption","a\nA🎵"},{"lyrics",""}}},
 {"valid_double_quotes","caption=\"piano\", lyrics=\"\"",true,true,{{"caption","piano"},{"lyrics",""}}},
 {"reordered_arguments","lyrics='', caption='piano'",false,true,{{"caption","piano"},{"lyrics",""}}},
 {"valid_escaped_enum","caption='piano', keyscale='\\u0043 major', lyrics=''",false,true,{{"caption","piano"},{"keyscale","C major"},{"lyrics",""}}},
 {"valid_duration_exponent","caption='piano', duration_seconds=1e2, lyrics=''",false,true,{{"caption","piano"},{"duration_seconds",100},{"lyrics",""}}},
 {"valid_duration_exponent_upper","caption='piano', duration_seconds=6e2, lyrics=''",false,true,{{"caption","piano"},{"duration_seconds",600},{"lyrics",""}}},
 {"pattern_invalid","caption='piano', lyrics='', timesignature='nonsense'",false,false,common_json()}
 };
 std::ofstream matrix(std::string(argv[2])+"/matrix.jsonl");int reds=0,rows=0;
 for(bool parallel:{false,true}){
  common_chat_templates_inputs inputs;common_chat_msg user;user.role="user";user.content="Synthetic offline formatter test.";inputs.messages.push_back(user);
  inputs.tools = common_chat_tools_parse_oaicompat(common_json::array({{{"type", "function"}, {"function", {{"name", "expand_sound"}, {"parameters", schema}, {"strict", true}}}}}));inputs.parallel_tool_calls=parallel;inputs.tool_choice=COMMON_CHAT_TOOL_CHOICE_REQUIRED;
  auto begin=std::chrono::steady_clock::now();
  auto p=common_chat_templates_apply(templates.get(),inputs);
  std::cout << "COST grammar_bytes=" << p.grammar.size() << " construction_us=" << std::chrono::duration_cast<std::chrono::microseconds>(std::chrono::steady_clock::now()-begin).count() << "\n";
  if(p.format!=COMMON_CHAT_FORMAT_PEG_NATIVE || p.grammar.empty() || p.grammar_lazy)throw std::runtime_error("specialized formatter not selected");
  for(auto &token:p.preserved_tokens)if(token=="<|tool_list_start|>")throw std::runtime_error("selected LFM2 instead of LFM2.5");
  std::string suffix=parallel?"true":"false";
  std::ofstream(std::string(argv[2])+"/grammar-parallel-"+suffix+".gbnf")<<p.grammar;
  std::ofstream(std::string(argv[2])+"/parser-parallel-"+suffix+".txt")<<p.parser.dump(p.parser.root());
  std::cout<<"FORMAT parallel="<<suffix<<" name="<<common_chat_format_name(p.format)<<" lazy="<<p.grammar_lazy<<" generation_prompt="<<common_json(p.generation_prompt).dump()<<"\n";
  if(!match(p.grammar,p.generation_prompt+"<|tool_call_start|>[expand_sound(caption='piano', lyrics='')]<|tool_call_end|>"))throw std::runtime_error("native recognizer positive control failed");
  if(match(p.grammar,p.generation_prompt+"<|tool_call_start|>[expand_sound(caption='piano', lyrics='')]<|tool_call_end|"))throw std::runtime_error("native recognizer truncated EOF control failed");
  auto run=[&](std::string name,std::string body,bool grammar_expected,bool parser_expected,common_json expected_json){
   std::string text="<|tool_call_start|>["+body+"]<|tool_call_end|>";
   // The generated root includes the assistant generation prefix; match from root, not sampler continuation.
   auto recognition_begin=std::chrono::steady_clock::now();
   bool recognized=match(p.grammar,p.generation_prompt+text);
   auto recognition_us=std::chrono::duration_cast<std::chrono::microseconds>(std::chrono::steady_clock::now()-recognition_begin).count();bool parsed=false,strict=false;size_t count=0;std::string error;common_json calls=common_json::array();
   common_peg_parse_context ctx(p.generation_prompt+text);auto result=p.parser.parse(ctx);strict=!result.fail()&&result.end==p.generation_prompt.size()+text.size();
   try{common_chat_parser_params pp(p);auto msg=common_chat_peg_parse(p.parser,common_chat_input(text),false,pp);count=msg.tool_calls.size();parsed=count>0;for(auto &c:msg.tool_calls)calls.push_back({{"name",c.name},{"arguments",common_json::parse(c.arguments)}});}catch(const std::exception &e){error=e.what();}
   bool exact_mapping=semantic_equals(calls,expected_json);
   bool red=recognized!=grammar_expected || strict!=parser_expected || !exact_mapping || (!parser_expected && count!=0);if(red)reds++;rows++;
   common_json row={{"parallel",parallel},{"fixture",name},{"grammar_expected",grammar_expected},{"parser_expected",parser_expected},{"expected_json",expected_json},{"exact_mapping",exact_mapping},{"recognition_us",recognition_us},{"grammar_accept",recognized},{"strict_peg_complete",strict},{"api_peg_tools",parsed},{"tool_count",count},{"calls",calls},{"error",error},{"red",red},{"text",text}};
   matrix<<row.dump()<<"\n";std::cout<<suffix<<"\t"<<name<<"\texpected="<<parser_expected<<"\tgrammar="<<recognized<<"\tstrictPEG="<<strict<<"\tapiTools="<<count<<"\t"<<(red?"RED":"OK")<<"\n";
  };
  auto expected_calls=[](common_json args){return common_json::array({common_json{{"name","expand_sound"},{"arguments",args}}});};
  for(const auto & v:schema["properties"]["keyscale"]["enum"]){
   run("enum_"+v.get<std::string>(),"expand_sound(caption='piano', keyscale="+v.dump()+", lyrics='')",true,true,expected_calls({{"caption","piano"},{"keyscale",v},{"lyrics",""}}));
  }
  for(auto &f:fixtures)run(f.name,"expand_sound("+f.args+")",f.grammar_expected,f.parser_expected,f.parser_expected?expected_calls(f.expected_json):common_json::array());
  run("two_calls","expand_sound(caption='piano', lyrics=''), expand_sound(caption='drums', lyrics='')",parallel,parallel,parallel?common_json::array({{{"name","expand_sound"},{"arguments",{{"caption","piano"},{"lyrics",""}}}},{{"name","expand_sound"},{"arguments",{{"caption","drums"},{"lyrics",""}}}}}):common_json::array());
 }
 std::cout<<"TOTAL rows="<<rows<<" contract_mismatches="<<reds<<" (offline; no model, inference, weights, GPU, or backend load)\n";
 return reds?2:0;
}catch(const std::exception &e){std::cerr<<"FATAL "<<e.what()<<"\n";return 1;}}
