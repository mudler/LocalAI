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
struct Fixture {std::string name,args;bool valid;};

int main(){ int failures=0; int checks=0;
auto comparison_check=[&](const char *name,const char *actual,const char *expected,bool equal){
 bool ok=semantic_equals(common_json::parse(actual),common_json::parse(expected))==equal;
 std::cout<<"comparison_"<<name<<" "<<(ok?"OK":"RED")<<"\n";if(!ok)++failures;
};
comparison_check("reordered_nested",R"({"b":[{"y":2,"x":1}],"a":0})",R"({"a":0,"b":[{"x":1,"y":2}]})",true);
comparison_check("changed_value",R"({"a":1})",R"({"a":2})",false);
comparison_check("missing_field",R"({})",R"({"a":1})",false);
comparison_check("extra_field",R"({"a":1,"b":2})",R"({"a":1})",false);
comparison_check("array_order",R"([1,2])",R"([2,1])",false);
comparison_check("numeric_semantics","100.0","100",true);
std::string tmpl="List of tools: [\n{% for tool in tools %}{{ tool | tojson }}\n{% endfor %}]\n{% for message in messages %}<|im_start|>{{ message.role }}\n{{ message.content }}<|im_end|>\n{% endfor %}{% if add_generation_prompt %}<|im_start|>assistant\n{% endif %}";
auto templates=common_chat_templates_init(nullptr,tmpl);
auto test=[&](std::string name, common_json schema,std::string args, bool grammar, bool parser, common_json expected, bool schema_error=false){ ++checks; try{
common_chat_templates_inputs in; common_chat_msg m;m.role="user";m.content="offline";in.messages.push_back(m);in.tool_choice=COMMON_CHAT_TOOL_CHOICE_REQUIRED;
in.tools=common_chat_tools_parse_oaicompat(common_json::array({{{"type","function"},{"function",{{"name","f"},{"parameters",schema},{"strict",true}}}}}));
auto p=common_chat_templates_apply(templates.get(),in);
auto text="<|tool_call_start|>[f("+args+")]<|tool_call_end|>";
common_peg_parse_context ctx(p.generation_prompt+text);auto r=p.parser.parse(ctx); bool complete=!r.fail()&&r.end==p.generation_prompt.size()+text.size();
bool recognized=match(p.grammar,p.generation_prompt+text);
bool mapped=false; size_t published=0;
try { common_chat_parser_params pp(p); auto msg=common_chat_peg_parse(p.parser,common_chat_input(text),false,pp); published=msg.tool_calls.size();
 mapped=msg.tool_calls.size()==1 && msg.tool_calls[0].name=="f" && semantic_equals(common_json::parse(msg.tool_calls[0].arguments),expected);
} catch(const std::exception &) {}
bool ok=!schema_error && recognized==grammar && complete==parser && mapped==parser && (parser || published==0);
std::cout<<name<<" grammar="<<recognized<<" peg="<<complete<<" exact_mapping="<<mapped<<" "<<(ok?"OK":"RED")<<"\n";
if(!ok)++failures;
}catch(const std::exception&e){ bool ok=schema_error && std::string(e.what()).find("strict native tool schema:")!=std::string::npos;
std::cout<<name<<" schema_error="<<e.what()<<" "<<(ok?"OK":"RED")<<"\n";if(!ok)++failures;}};
auto obj=[](common_json props,common_json req){return common_json{{"type","object"},{"additionalProperties",false},{"properties",props},{"required",req}};};
auto str=common_json{{"type","string"},{"minLength",1},{"maxLength",3}};
test("escaped_apostrophe_double",obj({{"a",str}},common_json::array({"a"})),"a=\"\\'\"",true,true,{{"a","'"}});
test("overflow_min",obj({{"a",{{"type","integer"},{"minimum",4294967297LL},{"maximum",4294967298LL}}}},common_json::array({"a"})),"a=1",false,false,{},true);
test("fraction_minLength",obj({{"a",{{"type","string"},{"minLength",1.9},{"maxLength",3}}}},common_json::array({"a"})),"a='x'",false,false,{},true);
test("negative_maxLength",obj({{"a",{{"type","string"},{"maxLength",-1}}}},common_json::array({"a"})),"a='xxxx'",false,false,{},true);
test("empty",obj(common_json::object(),common_json::array()),"",true,true,common_json::object());
test("optional_empty",obj({{"a",str}},common_json::array()),"",true,true,common_json::object());
test("optional_present",obj({{"a",str}},common_json::array()),"a='x'",true,true,{{"a","x"}});
test("unicode_enum",obj(common_json::parse(R"({"a":{"type":"string","enum":["A"]}})"),common_json::array({"a"})),"a='\\u0041'",false,true,{{"a","A"}});
test("reordered",obj({{"a",str},{"b",str}},common_json::array({"a","b"})),"b='x', a='y'",false,true,{{"a","y"},{"b","x"}});

auto num=common_json{{"type","number"},{"minimum",1},{"maximum",600}};
for(auto v:{"1e2","6e2"})test(std::string("exponent_")+v,obj({{"a",num}},common_json::array({"a"})),std::string("a=")+v,false,true,{{"a",v==std::string("1e2")?100:600}});
test("exponent_outside",obj({{"a",num}},common_json::array({"a"})),"a=1e3",false,false,{});
test("duplicate",obj({{"a",str}},common_json::array({"a"})),"a='x', a='y'",false,false,{});
test("prefix_names",obj({{"a",str},{"ab",str}},common_json::array({"a","ab"})),"a='x', ab='y'",true,true,{{"a","x"},{"ab","y"}});
test("unsupported",obj({{"a",{{"type","array"}}}},common_json::array({"a"})),"a=[]",false,false,{},true);

// Exercise the actual LFM parser/grammar and FINAL API mapper, not a surrogate.
// Lazy auto grammar constrains only the triggered tool segment; plain content
// is outside its scope. None deliberately builds a content-only parser.
for (auto mode : {COMMON_CHAT_TOOL_CHOICE_REQUIRED, COMMON_CHAT_TOOL_CHOICE_AUTO, COMMON_CHAT_TOOL_CHOICE_NONE}) {
 for (int strict_flag : {1, 0, -1}) {
  common_chat_templates_inputs in; common_chat_msg m; m.role="user"; m.content="offline"; in.messages.push_back(m);
  in.tool_choice=mode;
  common_json fn={{"name","f"},{"parameters",obj({{"a",strict_flag==1?str:common_json::object()}},common_json::array({"a"}))}};
  if(strict_flag>=0)fn["strict"]=strict_flag==1;
  in.tools=common_chat_tools_parse_oaicompat(common_json::array({{{"type","function"},{"function",fn}}}));
  auto p=common_chat_templates_apply(templates.get(),in);
  const bool none=mode==COMMON_CHAT_TOOL_CHOICE_NONE, automatic=mode==COMMON_CHAT_TOOL_CHOICE_AUTO;
  auto run_final=[&](const std::string &name,const std::string &text,bool grammar_expected,bool peg_expected,common_json expected,const std::string &content_expected="") {
   ++checks;
   // No grammar in none mode; record that as no recognition, not an accept.
   bool recognized=!p.grammar.empty() && match(p.grammar,(automatic?"":p.generation_prompt)+text);
   common_peg_parse_context ctx(p.generation_prompt+text);auto r=p.parser.parse(ctx);
   bool complete=!r.fail() && r.end==p.generation_prompt.size()+text.size();
   common_json calls=common_json::array();std::string content,error;
   try {common_chat_parser_params pp(p);auto msg=common_chat_peg_parse(p.parser,common_chat_input(text),false,pp);
    content=msg.content;for(auto &c:msg.tool_calls)calls.push_back({{"name",c.name},{"arguments",common_json::parse(c.arguments)}});
   }catch(const std::exception &e){error=e.what();}
   bool ok=recognized==grammar_expected && complete==peg_expected && semantic_equals(calls,expected) && (content_expected.empty() || content==content_expected);
   std::cout<<"FINAL mode="<<mode<<" strict="<<strict_flag<<" "<<name<<" grammar="<<recognized<<" peg="<<complete<<" calls="<<calls.dump()<<" content="<<common_json(content).dump()<<" error="<<error<<" "<<(ok?"OK":"RED")<<"\n";
   if(!ok)++failures;
  };
  auto call=[](common_json args){return common_json::array({{{"name","f"},{"arguments",args}}});};
  const std::string start="<|tool_call_start|>[",end="]<|tool_call_end|>";
  auto empty=common_json::array();
  run_final("complete",start+"f(a='x')"+end,!none,true,none?empty:call({{"a","x"}}),none?start+"f(a='x')"+end:"");
  run_final("missing_required",start+"f()"+end,false,none,empty);
  run_final("wrong_type",start+"f(a=7)"+end,!none && strict_flag!=1,none || strict_flag!=1,none || strict_flag==1?empty:call({{"a",7}}));
  run_final("plain","ordinary answer",false,none,empty,"ordinary answer");
  // Every byte prefix catches marker/name/equals/quote/escape/value/)/] EOFs.
  // The strict flat-string case and legacy nested-value case are both covered.
  std::string whole=start+(strict_flag==1?"f(a='x\\n')":"f(a={'nested': [1, {'key': 'x\\n'}]})")+end;
  for(size_t n=0;n<whole.size();++n){
   auto prefix=whole.substr(0,n);
   run_final("eof_byte_"+std::to_string(n),prefix,false,none,empty,none?prefix:"");
  }
 }
}
std::cout<<"EDGE checks="<<checks<<" failures="<<failures<<"\n"; return failures?2:0;
}
