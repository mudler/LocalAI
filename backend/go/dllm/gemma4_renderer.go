// Gemma4 (DiffusionGemma) chat template - NORMATIVE REFERENCE.
//
// The block comment below is the FULL `tokenizer.chat_template` extracted
// verbatim from diffusiongemma-26B-A4B-it-BF16.gguf via gguf-py's GGUFReader
// (17466 bytes, md5 8c34cf93c7a7815b3fdb300a009c4c17). Line numbers were
// added for citation only ("tpl L<n>" throughout this file); the template
// text itself is untouched. RenderGemma4 replicates this template
// byte-for-byte (verified against jinja2 renders and the transformers
// fixtures in tests/models/diffusion_gemma/test_modeling_diffusion_gemma.py),
// with ONE deliberate exception: the leading `{{- bos_token -}}` is NOT
// emitted - see the BOS NOTE after the template.
//
/*
     1	{%- macro format_parameters(properties, required, filter_keys=false) -%}
     2	    {%- set standard_keys = ['description', 'type', 'properties', 'required', 'nullable'] -%}
     3	    {%- set ns = namespace(found_first=false) -%}
     4	    {%- for key, value in properties | dictsort -%}
     5	        {%- set add_comma = false -%}
     6	        {%- if not filter_keys or key not in standard_keys -%}
     7	            {%- if ns.found_first %},{% endif -%}
     8	            {%- set ns.found_first = true -%}
     9	            {{ key }}:{
    10	            {%- if value['description'] -%}
    11	                description:<|"|>{{ value['description'] }}<|"|>
    12	                {%- set add_comma = true -%}
    13	            {%- endif -%}
    14	            {%- if value['type'] | upper == 'STRING' -%}
    15	                {%- if value['enum'] -%}
    16	                    {%- if add_comma %},{%- else -%} {%- set add_comma = true -%} {% endif -%}
    17	                    enum:{{ format_argument(value['enum']) }}
    18	                {%- endif -%}
    19	            {%- elif value['type'] | upper == 'ARRAY' -%}
    20	                {%- if value['items'] is mapping and value['items'] -%}
    21	                    {%- if add_comma %},{%- else -%} {%- set add_comma = true -%} {% endif -%}
    22	                    items:{
    23	                    {%- set ns_items = namespace(found_first=false) -%}
    24	                    {%- for item_key, item_value in value['items'] | dictsort -%}
    25	                        {%- if item_value is not none -%}
    26	                            {%- if ns_items.found_first %},{% endif -%}
    27	                            {%- set ns_items.found_first = true -%}
    28	                            {%- if item_key == 'properties' -%}
    29	                                properties:{
    30	                                {%- if item_value is mapping -%}
    31	                                    {{- format_parameters(item_value, value['items']['required'] | default([])) -}}
    32	                                {%- endif -%}
    33	                                }
    34	                            {%- elif item_key == 'required' -%}
    35	                                required:[
    36	                                {%- for req_item in item_value -%}
    37	                                    <|"|>{{- req_item -}}<|"|>
    38	                                    {%- if not loop.last %},{% endif -%}
    39	                                {%- endfor -%}
    40	                                ]
    41	                            {%- elif item_key == 'type' -%}
    42	                                {%- if item_value is string -%}
    43	                                    type:{{ format_argument(item_value | upper) }}
    44	                                {%- else -%}
    45	                                    type:{{ format_argument(item_value | map('upper') | list) }}
    46	                                {%- endif -%}
    47	                            {%- else -%}
    48	                                {{ item_key }}:{{ format_argument(item_value) }}
    49	                            {%- endif -%}
    50	                        {%- endif -%}
    51	                    {%- endfor -%}
    52	                    }
    53	                {%- endif -%}
    54	            {%- endif -%}
    55	            {%- if value['nullable'] %}
    56	                {%- if add_comma %},{%- else -%} {%- set add_comma = true -%} {% endif -%}
    57	                nullable:true
    58	            {%- endif -%}
    59	            {%- if value['type'] | upper == 'OBJECT' -%}
    60	                {%- if value['properties'] is defined and value['properties'] is mapping -%}
    61	                    {%- if add_comma %},{%- else -%} {%- set add_comma = true -%} {% endif -%}
    62	                    properties:{
    63	                    {{- format_parameters(value['properties'], value['required'] | default([])) -}}
    64	                    }
    65	                {%- elif value is mapping -%}
    66	                    {%- if add_comma %},{%- else -%} {%- set add_comma = true -%} {% endif -%}
    67	                    properties:{
    68	                    {{- format_parameters(value, value['required'] | default([]), filter_keys=true) -}}
    69	                    }
    70	                {%- endif -%}
    71	                {%- if value['required'] -%}
    72	                    {%- if add_comma %},{%- else -%} {%- set add_comma = true -%} {% endif -%}
    73	                    required:[
    74	                    {%- for item in value['required'] | default([]) -%}
    75	                        <|"|>{{- item -}}<|"|>
    76	                        {%- if not loop.last %},{% endif -%}
    77	                    {%- endfor -%}
    78	                    ]
    79	                {%- endif -%}
    80	            {%- endif -%}
    81	            {%- if add_comma %},{%- else -%} {%- set add_comma = true -%} {% endif -%}
    82	            type:<|"|>{{ value['type'] | upper }}<|"|>}
    83	        {%- endif -%}
    84	    {%- endfor -%}
    85	{%- endmacro -%}
    86	{%- macro format_function_declaration(tool_data) -%}
    87	    declaration:{{- tool_data['function']['name'] -}}{description:<|"|>{{- tool_data['function']['description'] -}}<|"|>
    88	    {%- set params = tool_data['function']['parameters'] -%}
    89	    {%- if params -%}
    90	        ,parameters:{
    91	        {%- if params['properties'] -%}
    92	            properties:{ {{- format_parameters(params['properties'], params['required']) -}} },
    93	        {%- endif -%}
    94	        {%- if params['required'] -%}
    95	            required:[
    96	            {%- for item in params['required'] -%}
    97	                <|"|>{{- item -}}<|"|>
    98	                {{- ',' if not loop.last -}}
    99	            {%- endfor -%}
   100	            ],
   101	        {%- endif -%}
   102	        {%- if params['type'] -%}
   103	            type:<|"|>{{- params['type'] | upper -}}<|"|>}
   104	        {%- endif -%}
   105	    {%- endif -%}
   106	    {%- if 'response' in tool_data['function'] -%}
   107	        {%- set response_declaration = tool_data['function']['response'] -%}
   108	        ,response:{
   109	        {%- if response_declaration['description'] -%}
   110	            description:<|"|>{{- response_declaration['description'] -}}<|"|>,
   111	        {%- endif -%}
   112	        {%- if response_declaration['type'] | upper == 'OBJECT' -%}
   113	            type:<|"|>{{- response_declaration['type'] | upper -}}<|"|>}
   114	        {%- endif -%}
   115	    {%- endif -%}
   116	    }
   117	{%- endmacro -%}
   118	{%- macro format_argument(argument, escape_keys=True) -%}
   119	    {%- if argument is string -%}
   120	        {{- '<|"|>' + argument + '<|"|>' -}}
   121	    {%- elif argument is boolean -%}
   122	        {{- 'true' if argument else 'false' -}}
   123	    {%- elif argument is mapping -%}
   124	        {{- '{' -}}
   125	        {%- set ns = namespace(found_first=false) -%}
   126	        {%- for key, value in argument | dictsort -%}
   127	            {%- if ns.found_first %},{% endif -%}
   128	            {%- set ns.found_first = true -%}
   129	            {%- if escape_keys -%}
   130	                {{- '<|"|>' + key + '<|"|>' -}}
   131	            {%- else -%}
   132	                {{- key -}}
   133	            {%- endif -%}
   134	            :{{- format_argument(value, escape_keys=escape_keys) -}}
   135	        {%- endfor -%}
   136	        {{- '}' -}}
   137	    {%- elif argument is sequence -%}
   138	        {{- '[' -}}
   139	        {%- for item in argument -%}
   140	            {{- format_argument(item, escape_keys=escape_keys) -}}
   141	            {%- if not loop.last %},{% endif -%}
   142	        {%- endfor -%}
   143	        {{- ']' -}}
   144	    {%- else -%}
   145	        {{- argument -}}
   146	    {%- endif -%}
   147	{%- endmacro -%}
   148	{%- macro strip_thinking(text) -%}
   149	    {%- set ns = namespace(result='') -%}
   150	    {%- for part in text.split('<channel|>') -%}
   151	        {%- if '<|channel>' in part -%}
   152	            {%- set ns.result = ns.result + part.split('<|channel>')[0] -%}
   153	        {%- else -%}
   154	            {%- set ns.result = ns.result + part -%}
   155	        {%- endif -%}
   156	    {%- endfor -%}
   157	    {{- ns.result | trim -}}
   158	{%- endmacro -%}
   159
   160	{%- macro format_tool_response_block(tool_name, response) -%}
   161	    {{- '<|tool_response>' -}}
   162	    {%- if response is mapping -%}
   163	        {{- 'response:' + tool_name + '{' -}}
   164	        {%- for key, value in response | dictsort -%}
   165	            {{- key -}}:{{- format_argument(value, escape_keys=False) -}}
   166	            {%- if not loop.last %},{% endif -%}
   167	        {%- endfor -%}
   168	        {{- '}' -}}
   169	    {%- else -%}
   170	        {{- 'response:' + tool_name + '{value:' + format_argument(response, escape_keys=False) + '}' -}}
   171	    {%- endif -%}
   172	    {{- '<tool_response|>' -}}
   173	{%- endmacro -%}
   174
   175	{%- set ns = namespace(prev_message_type=None) -%}
   176	{%- set loop_messages = messages -%}
   177	{{- bos_token -}}
   178	{#- Handle System/Tool Definitions Block -#}
   179	{%- if (enable_thinking is defined and enable_thinking) or tools or messages[0]['role'] in ['system', 'developer'] -%}
   180	    {{- '<|turn>system\n' -}}
   181	    {#- Inject Thinking token at the very top of the FIRST system turn -#}
   182	    {%- if enable_thinking is defined and enable_thinking -%}
   183	        {{- '<|think|>\n' -}}
   184	        {%- set ns.prev_message_type = 'think' -%}
   185	    {%- endif -%}
   186	    {%- if messages[0]['role'] in ['system', 'developer'] -%}
   187	        {%- if messages[0]['content'] is string -%}
   188	            {{- messages[0]['content'] | trim -}}
   189	        {%- elif messages[0]['content'] is sequence -%}
   190	            {%- for item in messages[0]['content'] -%}
   191	                {{- item['text'] | trim + ' '-}}
   192	            {%- endfor -%}
   193	        {%- endif -%}
   194	        {%- set loop_messages = messages[1:] -%}
   195	    {%- endif -%}
   196	    {%- if tools -%}
   197	        {%- for tool in tools %}
   198	            {{- '<|tool>' -}}
   199	            {{- format_function_declaration(tool) | trim -}}
   200	            {{- '<tool|>' -}}
   201	        {%- endfor %}
   202	        {%- set ns.prev_message_type = 'tool' -%}
   203	    {%- endif -%}
   204	    {{- '<turn|>\n' -}}
   205	{%- endif %}
   206
   207	{#- Pre-scan: find last user message index for reasoning guard -#}
   208	{%- set ns_turn = namespace(last_user_idx=-1) -%}
   209	{%- for i in range(loop_messages | length) -%}
   210	    {%- if loop_messages[i]['role'] == 'user' -%}
   211	        {%- set ns_turn.last_user_idx = i -%}
   212	    {%- endif -%}
   213	{%- endfor -%}
   214
   215	{#- Loop through messages -#}
   216	{%- for message in loop_messages -%}
   217	    {%- if message['role'] != 'tool' -%}
   218	    {%- set ns.prev_message_type = None -%}
   219	    {%- set role = 'model' if message['role'] == 'assistant' else message['role'] -%}
   220	    {#- Detect continuation: suppress duplicate <|turn>model when previous non-tool message was also assistant -#}
   221	    {%- set prev_nt = namespace(role=None, found=false) -%}
   222	    {%- if loop.index0 > 0 -%}
   223	        {%- for j in range(loop.index0 - 1, -1, -1) -%}
   224	            {%- if not prev_nt.found -%}
   225	                {%- if loop_messages[j]['role'] != 'tool' -%}
   226	                    {%- set prev_nt.role = loop_messages[j]['role'] -%}
   227	                    {%- set prev_nt.found = true -%}
   228	                {%- endif -%}
   229	            {%- endif -%}
   230	        {%- endfor -%}
   231	    {%- endif -%}
   232	    {%- set continue_same_model_turn = (role == 'model' and prev_nt.role == 'assistant') -%}
   233	    {%- if not continue_same_model_turn -%}
   234	        {{- '<|turn>' + role + '\n' }}
   235	    {%- endif -%}
   236
   237	    {#- Render reasoning/reasoning_content as thinking channel -#}
   238	    {%- set thinking_text = message.get('reasoning') or message.get('reasoning_content') -%}
   239	    {%- if thinking_text and loop.index0 > ns_turn.last_user_idx and message.get('tool_calls') -%}
   240	        {{- '<|channel>thought\n' + thinking_text + '\n<channel|>' -}}
   241	    {%- endif -%}
   242
   243	            {%- if message['tool_calls'] -%}
   244	                {%- for tool_call in message['tool_calls'] -%}
   245	                    {%- set function = tool_call['function'] -%}
   246	                    {{- '<|tool_call>call:' + function['name'] + '{' -}}
   247	                    {%- if function['arguments'] is mapping -%}
   248	                        {%- set ns_args = namespace(found_first=false) -%}
   249	                        {%- for key, value in function['arguments'] | dictsort -%}
   250	                            {%- if ns_args.found_first %},{% endif -%}
   251	                            {%- set ns_args.found_first = true -%}
   252	                            {{- key -}}:{{- format_argument(value, escape_keys=False) -}}
   253	                        {%- endfor -%}
   254	                    {%- elif function['arguments'] is string -%}
   255	                        {{- function['arguments'] -}}
   256	                    {%- endif -%}
   257	                    {{- '}<tool_call|>' -}}
   258	                {%- endfor -%}
   259	                {%- set ns.prev_message_type = 'tool_call' -%}
   260	            {%- endif -%}
   261
   262	            {%- set ns_tr_out = namespace(flag=false) -%}
   263	            {%- if message.get('tool_responses') -%}
   264	                {#- Legacy: tool_responses embedded on the assistant message (Google/Gemma native) -#}
   265	                {%- for tool_response in message['tool_responses'] -%}
   266	                    {{- format_tool_response_block(tool_response['name'] | default('unknown'), tool_response['response']) -}}
   267	                    {%- set ns_tr_out.flag = true -%}
   268	                    {%- set ns.prev_message_type = 'tool_response' -%}
   269	                {%- endfor -%}
   270	            {%- elif message.get('tool_calls') -%}
   271	                {#- OpenAI Chat Completions: forward-scan consecutive role:tool messages -#}
   272	                {%- set ns_tool_scan = namespace(stopped=false) -%}
   273	                {%- for k in range(loop.index0 + 1, loop_messages | length) -%}
   274	                    {%- if ns_tool_scan.stopped -%}
   275	                    {%- elif loop_messages[k]['role'] != 'tool' -%}
   276	                        {%- set ns_tool_scan.stopped = true -%}
   277	                    {%- else -%}
   278	                        {%- set follow = loop_messages[k] -%}
   279	                        {#- Resolve tool_call_id to function name -#}
   280	                        {%- set ns_tname = namespace(name=follow.get('name') | default('unknown')) -%}
   281	                        {%- for tc in message['tool_calls'] -%}
   282	                            {%- if tc.get('id') == follow.get('tool_call_id') -%}
   283	                                {%- set ns_tname.name = tc['function']['name'] -%}
   284	                            {%- endif -%}
   285	                        {%- endfor -%}
   286	                        {#- Handle content as string or content-parts array -#}
   287	                        {%- set tool_body = follow.get('content') -%}
   288	                        {%- if tool_body is string -%}
   289	                            {{- format_tool_response_block(ns_tname.name, tool_body) -}}
   290	                        {%- elif tool_body is sequence and tool_body is not string -%}
   291	                            {%- set ns_txt = namespace(s='') -%}
   292	                            {%- for part in tool_body -%}
   293	                                {%- if part.get('type') == 'text' -%}
   294	                                    {%- set ns_txt.s = ns_txt.s + (part.get('text') | default('')) -%}
   295	                                {%- endif -%}
   296	                            {%- endfor -%}
   297	                            {{- format_tool_response_block(ns_tname.name, ns_txt.s) -}}
   298	                            {%- for part in tool_body -%}
   299	                                {%- if part.get('type') == 'image' -%}
   300	                                    {{- '<|image|>' -}}
   301	                                {%- elif part.get('type') == 'audio' -%}
   302	                                    {{- '<|audio|>' -}}
   303	                                {%- elif part.get('type') == 'video' -%}
   304	                                    {{- '<|video|>' -}}
   305	                                {%- endif -%}
   306	                            {%- endfor -%}
   307	                        {%- else -%}
   308	                            {{- format_tool_response_block(ns_tname.name, tool_body) -}}
   309	                        {%- endif -%}
   310	                        {%- set ns_tr_out.flag = true -%}
   311	                        {%- set ns.prev_message_type = 'tool_response' -%}
   312	                    {%- endif -%}
   313	                {%- endfor -%}
   314	            {%- endif -%}
   315
   316	            {%- set captured_content -%}
   317	            {%- if message['content'] is string -%}
   318	                {%- if role == 'model' -%}
   319	                    {{- strip_thinking(message['content']) -}}
   320	                {%- else -%}
   321	                    {{- message['content'] | trim -}}
   322	                {%- endif -%}
   323	            {%- elif message['content'] is sequence -%}
   324	                {%- for item in message['content'] -%}
   325	                    {%- if item['type'] == 'text' -%}
   326	                        {%- if role == 'model' -%}
   327	                            {{- strip_thinking(item['text']) -}}
   328	                        {%- else -%}
   329	                            {{- item['text'] | trim -}}
   330	                        {%- endif -%}
   331	                    {%- elif item['type'] == 'image' -%}
   332	                        {{- '<|image|>' -}}
   333	                        {%- set ns.prev_message_type = 'image' -%}
   334	                    {%- elif item['type'] == 'audio' -%}
   335	                        {{- '<|audio|>' -}}
   336	                        {%- set ns.prev_message_type = 'audio' -%}
   337	                    {%- elif item['type'] == 'video' -%}
   338	                        {{- '<|video|>' -}}
   339	                        {%- set ns.prev_message_type = 'video' -%}
   340	                    {%- endif -%}
   341	                {%- endfor -%}
   342	            {%- endif -%}
   343	            {%- endset -%}
   344
   345	            {{- captured_content -}}
   346	            {%- set has_content = captured_content | trim | length > 0 -%}
   347
   348	        {%- if ns.prev_message_type == 'tool_call' and not ns_tr_out.flag -%}
   349	            {{- '<|tool_response>' -}}
   350	        {%- elif not (ns_tr_out.flag and not has_content) -%}
   351	            {{- '<turn|>\n' -}}
   352	        {%- endif -%}
   353	    {%- endif -%}
   354	{%- endfor -%}
   355
   356	{%- if add_generation_prompt -%}
   357	    {%- if ns.prev_message_type != 'tool_response' and ns.prev_message_type != 'tool_call' -%}
   358	        {{- '<|turn>model\n' -}}
   359	        {%- if not enable_thinking | default(false) -%}
   360	            {{- '<|channel>thought\n<channel|>' -}}
   361	        {%- endif -%}
   362	    {%- endif -%}
   363	{%- endif -%}*/

// Every rule below cites "tpl L<n>" line numbers from the numbered template
// text above.
//
// BOS NOTE (tpl L177 `{{- bos_token -}}`): the template emits <bos> because
// HF's apply_chat_template is expected to produce the FULL token stream. Our
// renderer feeds dllm_capi_generate, whose run_generate tokenizes with
// prepend_bos = vocab.add_bos (dllm.cpp src/capi.cpp:230-231), and gemma4
// GGUFs carry add_bos=true - the C side prepends BOS itself. A literal
// "<bos>" here would therefore double it, so RenderGemma4 NEVER emits it.

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	pb "github.com/mudler/LocalAI/pkg/grpc/proto"
)

// Gemma4 marker vocabulary (special tokens referenced by the template).
const (
	gemma4StringDelim = `<|"|>`   // string delimiter, tpl L119 etc.
	gemma4TurnOpen    = "<|turn>" // tpl L180/L234/L358
	// gemma4TurnEnd is the turn terminator as the MODEL emits it: the output
	// parser (gemma4_parser.go) must trigger on the bare token, while the
	// renderer appends the template's inter-turn newline (gemma4TurnClose).
	gemma4TurnEnd           = "<turn|>"            // tpl L204/L351
	gemma4TurnClose         = gemma4TurnEnd + "\n" // tpl L204/L351
	gemma4ThinkToken        = "<|think|>\n"        // tpl L183
	gemma4ToolOpen          = "<|tool>"            // tpl L198
	gemma4ToolClose         = "<tool|>"            // tpl L200
	gemma4ToolCallOpen      = "<|tool_call>"       // tpl L246
	gemma4ToolCallClose     = "<tool_call|>"       // tpl L257
	gemma4ToolResponseOpen  = "<|tool_response>"   // tpl L161/L349
	gemma4ToolResponseClose = "<tool_response|>"   // tpl L172
	gemma4ChannelOpen       = "<|channel>"         // tpl L240/L360
	gemma4ChannelClose      = "<channel|>"         // tpl L240/L360
	gemma4ThoughtChannel    = gemma4ChannelOpen + "thought\n"
)

// gemma4ToolCall is the wire shape LocalAI core puts into pb.Message.ToolCalls
// (core/schema/message.go ToolCall marshalled by Messages.ToProto): a JSON
// array of {"index":..,"id":..,"type":..,"function":{"name":..,"arguments":..}}.
type gemma4ToolCall struct {
	ID       string `json:"id"`
	Function struct {
		Name string `json:"name"`
		// Arguments is a JSON-encoded string in the OpenAI wire format
		// (schema.FunctionCall.Arguments is a string), but kept raw here so a
		// template-native object also works. See renderGemma4ToolCallArgs.
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

// RenderGemma4 renders an OpenAI-style message list (plus the request's tools
// JSON array) into the gemma4 prompt string, replicating the GGUF chat
// template above byte-for-byte - except for the leading <bos> (see BOS NOTE).
//
// enableThinking maps to the template's enable_thinking flag (ds4 convention:
// Metadata["enable_thinking"]); addGenerationPrompt to add_generation_prompt.
//
// IMAGE NOTE (tpl L323-L342): the template's content-parts branch renders
// one <|image|> token per image part, at the part's position. Through pb
// that branch is unreachable: LocalAI's OpenAI layer flattens content parts
// before the backend sees them - text parts are concatenated into
// pb.Message.Content (core/schema/message.go ToProto) and image parts are
// decoded to raw base64 in PredictOptions.Images (core/http/middleware/
// request.go), losing per-message attribution and intra-message position.
// The llama.cpp backend's convention for the same flattened delivery is to
// attach ALL request images to the LAST user message, text first then
// images (grpc-server.cpp, "Add text first" in the last-user-msg branch);
// nImages mirrors that: one marker per image appended directly after the
// last user message's text, in image order (the template emits parts
// back-to-back with no separator either). The marker emitted is the ENGINE
// splice marker mmImageMarker ("<image>", dllm_capi.h placeholder
// contract), NOT the template's <|image|> text token: the engine expands
// "<image>" to <boi> + soft-token placeholders + <eoi> and splices the
// vision embeddings there, whereas a literal <|image|> would just tokenize
// as text and leave a marker/image count mismatch.
func RenderGemma4(msgs []*pb.Message, toolsJSON string, nImages int, enableThinking bool, addGenerationPrompt bool) (string, error) {
	// Fail loud on roles the template does not know about. The jinja would
	// happily render any role as a generic turn; we reject instead so typos
	// surface at the API boundary rather than as silent bad prompts.
	for i, m := range msgs {
		switch m.GetRole() {
		case "system", "developer", "user", "assistant", "tool":
		default:
			return "", fmt.Errorf("dllm: gemma4 renderer: unknown role %q in message %d", m.GetRole(), i)
		}
	}

	tools, err := parseGemma4Tools(toolsJSON)
	if err != nil {
		return "", err
	}

	var b strings.Builder
	// ns.prev_message_type (tpl L175); "" stands for jinja None.
	prev := ""

	// System/tool-definitions block (tpl L178-L205).
	loopMsgs := msgs
	firstIsSystem := len(msgs) > 0 && (msgs[0].GetRole() == "system" || msgs[0].GetRole() == "developer")
	if enableThinking || len(tools) > 0 || firstIsSystem {
		b.WriteString(gemma4TurnOpen + "system\n") // tpl L180
		if enableThinking {
			// Thinking token at the very top of the first system turn,
			// tpl L182-L185. NOTE: prev_message_type='think' (not used by
			// the ending logic, mirrored for fidelity).
			b.WriteString(gemma4ThinkToken)
			prev = "think"
		}
		if firstIsSystem {
			// First system/developer message is folded into this turn and
			// consumed (loop_messages = messages[1:]), tpl L186-L195.
			// pb.Message.Content is always a flattened string (core/schema/
			// message.go ToProto), so only the `is string` branch applies.
			b.WriteString(strings.TrimSpace(msgs[0].GetContent()))
			loopMsgs = msgs[1:]
		}
		if len(tools) > 0 {
			// One <|tool>declaration:...<tool|> block per tool, tpl L196-L203.
			for _, t := range tools {
				b.WriteString(gemma4ToolOpen)
				b.WriteString(strings.TrimSpace(formatGemma4FunctionDeclaration(t)))
				b.WriteString(gemma4ToolClose)
			}
			prev = "tool"
		}
		b.WriteString(gemma4TurnClose) // tpl L204
	}

	// Pre-scan: last user message index for the reasoning guard, tpl L207-L213
	// (also the image attachment point - see the IMAGE NOTE).
	lastUserIdx := -1
	for i, m := range loopMsgs {
		if m.GetRole() == "user" {
			lastUserIdx = i
		}
	}
	if nImages > 0 && lastUserIdx == -1 {
		// No user turn to attach the markers to: the engine would reject the
		// markerless prompt anyway (marker/image count mismatch), so surface
		// the bad request here with a usable message.
		return "", fmt.Errorf("dllm: gemma4 renderer: %d image(s) provided but no user message to attach them to", nImages)
	}

	// Message loop, tpl L215-L354. role=tool messages are skipped here: they
	// are rendered by the forward-scan from their assistant tool_calls turn.
	// consumedTool tracks which of them a forward-scan actually rendered, so
	// an orphan tool message (no preceding assistant tool_calls turn) fails
	// loud below instead of vanishing from the prompt.
	consumedTool := make([]bool, len(loopMsgs))
	for i, m := range loopMsgs {
		if m.GetRole() == "tool" {
			continue
		}
		prev = "" // tpl L218
		role := m.GetRole()
		if role == "assistant" {
			role = "model" // tpl L219
		}

		// Continuation: suppress duplicate <|turn>model when the previous
		// non-tool message was also assistant, tpl L220-L235.
		prevNonToolRole := ""
		for j := i - 1; j >= 0; j-- {
			if loopMsgs[j].GetRole() != "tool" {
				prevNonToolRole = loopMsgs[j].GetRole()
				break
			}
		}
		if !(role == "model" && prevNonToolRole == "assistant") {
			b.WriteString(gemma4TurnOpen + role + "\n")
		}

		var toolCalls []gemma4ToolCall
		if tc := m.GetToolCalls(); strings.TrimSpace(tc) != "" {
			if err := json.Unmarshal([]byte(tc), &toolCalls); err != nil {
				return "", fmt.Errorf("dllm: gemma4 renderer: message %d: invalid tool_calls JSON: %w", i, err)
			}
		}

		// reasoning_content renders as a thought channel ONLY on the
		// tool-calling turn after the last user message, tpl L237-L241.
		if rc := m.GetReasoningContent(); rc != "" && i > lastUserIdx && len(toolCalls) > 0 {
			b.WriteString(gemma4ThoughtChannel + rc + "\n" + gemma4ChannelClose)
		}

		// Tool calls: <|tool_call>call:name{args}<tool_call|>, concatenated
		// without separators, tpl L243-L260.
		if len(toolCalls) > 0 {
			for _, tc := range toolCalls {
				b.WriteString(gemma4ToolCallOpen + "call:" + tc.Function.Name + "{")
				b.WriteString(renderGemma4ToolCallArgs(tc.Function.Arguments))
				b.WriteString("}" + gemma4ToolCallClose)
			}
			prev = "tool_call"
		}

		// Tool responses: pb has no legacy tool_responses field (tpl
		// L263-L269 is unreachable through proto), so only the OpenAI
		// forward-scan of consecutive role=tool messages applies,
		// tpl L270-L313.
		trOut := false
		if len(toolCalls) > 0 {
			for k := i + 1; k < len(loopMsgs); k++ {
				if loopMsgs[k].GetRole() != "tool" {
					break
				}
				follow := loopMsgs[k]
				// Resolve tool_call_id to the function name; the message's
				// own name (default 'unknown') is the fallback, tpl L278-L285.
				name := follow.GetName()
				if name == "" {
					name = "unknown"
				}
				for _, tc := range toolCalls {
					if tc.ID == follow.GetToolCallId() {
						name = tc.Function.Name
					}
				}
				// pb content is a flattened string: only the string body
				// branch (tpl L287-L289) is reachable.
				b.WriteString(formatGemma4ToolResponseBlock(name, follow.GetContent()))
				consumedTool[k] = true
				trOut = true
				prev = "tool_response"
			}
		}

		// Captured content, tpl L316-L345. Model content gets thinking
		// channels stripped (strip_thinking, tpl L148-L158); other roles are
		// trimmed. pb content is a flattened string: the content-parts array
		// branch (tpl L322-L342) is unreachable through it - the image part
		// of that branch is reconstructed below from PredictOptions.Images
		// (see the IMAGE NOTE on RenderGemma4).
		var content string
		if role == "model" {
			content = stripGemma4Thinking(m.GetContent())
		} else {
			content = strings.TrimSpace(m.GetContent())
		}
		if i == lastUserIdx && nImages > 0 {
			// Markers are part of captured_content in the template (an
			// image-only message still counts as has_content and closes its
			// turn), so append before the hasContent computation.
			content += strings.Repeat(mmImageMarker, nImages)
		}
		b.WriteString(content)
		hasContent := strings.TrimSpace(content) != "" // tpl L346

		// Turn ending, tpl L348-L353: a tool_calls turn with no rendered
		// responses ends on an OPEN <|tool_response> (the runtime fills it);
		// a turn whose only payload was tool responses stays open (no
		// <turn|>); everything else closes the turn.
		if prev == "tool_call" && !trOut {
			b.WriteString(gemma4ToolResponseOpen)
		} else if !(trOut && !hasContent) {
			b.WriteString(gemma4TurnClose)
		}
	}

	// Fail loud on orphan role:tool messages no forward-scan consumed (e.g. a
	// tool message with no preceding assistant tool_calls turn): the jinja
	// would silently drop them from the prompt; we surface the bad request
	// instead, same philosophy as the unknown-role check above.
	for i, m := range loopMsgs {
		if m.GetRole() == "tool" && !consumedTool[i] {
			return "", fmt.Errorf("dllm: gemma4 renderer: orphan tool message %d: no preceding assistant tool_calls turn consumed it", i+(len(msgs)-len(loopMsgs)))
		}
	}

	// Generation prompt, tpl L356-L362: never reopened right after a
	// tool_call/tool_response (the model continues its own open turn); the
	// thought channel is pre-opened only when thinking is NOT enabled.
	if addGenerationPrompt && prev != "tool_response" && prev != "tool_call" {
		b.WriteString(gemma4TurnOpen + "model\n")
		if !enableThinking {
			b.WriteString(gemma4ThoughtChannel + gemma4ChannelClose)
		}
	}
	return b.String(), nil
}

// parseGemma4Tools decodes the request's OpenAI tools JSON array
// ([{"type":"function","function":{...}}]). Numbers are kept as json.Number
// so 42 / 3.5 / 1.0 render exactly as jinja renders the Python values.
// An empty/null/[] input is jinja-falsy (tpl L196 `{%- if tools -%}`).
func parseGemma4Tools(toolsJSON string) ([]map[string]any, error) {
	s := strings.TrimSpace(toolsJSON)
	if s == "" || s == "null" {
		return nil, nil
	}
	v, err := decodeGemma4JSON([]byte(s))
	if err != nil {
		return nil, fmt.Errorf("dllm: gemma4 renderer: invalid tools JSON: %w", err)
	}
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("dllm: gemma4 renderer: tools JSON is not an array")
	}
	tools := make([]map[string]any, 0, len(list))
	for i, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("dllm: gemma4 renderer: tools[%d] is not an object", i)
		}
		tools = append(tools, m)
	}
	return tools, nil
}

// decodeGemma4JSON unmarshals with UseNumber so numeric literals survive
// verbatim ("1.0" stays "1.0", matching jinja's rendering of Python 1.0).
// Trailing non-whitespace after the first value is rejected: json.Decoder
// stops at the value boundary, and silently ignoring the rest would render
// a prompt from a prefix of what the caller sent.
func decodeGemma4JSON(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("trailing data after JSON value")
	}
	return v, nil
}

// renderGemma4ToolCallArgs renders the arguments between the braces of
// call:name{...}, tpl L247-L256: a mapping renders as dictsorted
// key:format_argument(value, escape_keys=False) pairs; a string renders
// verbatim; anything else renders nothing (mirroring the if/elif).
//
// DIVERGENCE NOTE: through pb the arguments arrive as a JSON-encoded string
// (OpenAI wire format; schema.FunctionCall.Arguments is a string). HF/vLLM
// parse that string into a dict before applying the template, so we do the
// same: a string that parses as a JSON object takes the mapping branch; only
// a non-object string falls back to the template's verbatim string branch.
//
// Also note: string values containing the literal <|"|> delimiter render
// unescaped (prompt-structure injection), byte-faithful to the jinja which
// has identical behavior.
func renderGemma4ToolCallArgs(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	v, err := decodeGemma4JSON(raw)
	if err != nil {
		// Not JSON at all: treat like the template's string branch on the
		// raw bytes (never drop caller data silently).
		return string(raw)
	}
	if s, ok := v.(string); ok {
		inner, err := decodeGemma4JSON([]byte(s))
		if err == nil {
			if m, ok := inner.(map[string]any); ok {
				v = m
			} else {
				return s // tpl L253-L254: string renders verbatim
			}
		} else {
			return s
		}
	}
	m, ok := v.(map[string]any)
	if !ok {
		return "" // tpl L247-L255: non-mapping, non-string renders nothing
	}
	parts := make([]string, 0, len(m))
	for _, k := range gemma4DictsortKeys(m) {
		parts = append(parts, k+":"+formatGemma4Argument(m[k], false))
	}
	return strings.Join(parts, ",")
}

// formatGemma4Argument is the format_argument macro, tpl L118-L147:
// strings get <|"|> delimiters, booleans lower-case, mappings dictsorted
// {key:value} (keys delimited only when escape_keys), sequences [..],
// everything else verbatim (json.Number keeps its literal; null renders
// "None" exactly as jinja renders Python None).
func formatGemma4Argument(v any, escapeKeys bool) string {
	switch a := v.(type) {
	case string:
		return gemma4StringDelim + a + gemma4StringDelim
	case bool:
		if a {
			return "true"
		}
		return "false"
	case map[string]any:
		var b strings.Builder
		b.WriteString("{")
		for i, k := range gemma4DictsortKeys(a) {
			if i > 0 {
				b.WriteString(",")
			}
			if escapeKeys {
				b.WriteString(gemma4StringDelim + k + gemma4StringDelim)
			} else {
				b.WriteString(k)
			}
			b.WriteString(":" + formatGemma4Argument(a[k], escapeKeys))
		}
		b.WriteString("}")
		return b.String()
	case []any:
		var b strings.Builder
		b.WriteString("[")
		for i, item := range a {
			if i > 0 {
				b.WriteString(",")
			}
			b.WriteString(formatGemma4Argument(item, escapeKeys))
		}
		b.WriteString("]")
		return b.String()
	case json.Number:
		return a.String()
	case nil:
		return "None" // jinja renders Python None as "None"
	default:
		return fmt.Sprint(a)
	}
}

// gemma4DictsortKeys mirrors jinja's dictsort default: case-insensitive sort
// by key. Distinct keys equal under lowering tie-break on the raw key for
// determinism (Go maps have no insertion order to preserve).
func gemma4DictsortKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		li, lj := strings.ToLower(keys[i]), strings.ToLower(keys[j])
		if li != lj {
			return li < lj
		}
		return keys[i] < keys[j]
	})
	return keys
}

// gemma4Lookup is jinja's value['key'] on a value of unknown type: missing
// keys and non-mapping receivers yield Undefined (nil here).
func gemma4Lookup(v any, key string) any {
	if m, ok := v.(map[string]any); ok {
		return m[key]
	}
	return nil
}

// gemma4Truthy is jinja truthiness for the decoded JSON value set.
func gemma4Truthy(v any) bool {
	switch a := v.(type) {
	case nil:
		return false
	case bool:
		return a
	case string:
		return a != ""
	case json.Number:
		f, err := a.Float64()
		return err != nil || f != 0
	case map[string]any:
		return len(a) > 0
	case []any:
		return len(a) > 0
	default:
		return true
	}
}

// gemma4Str renders a scalar the way `{{ value }}` would (Undefined -> "").
func gemma4Str(v any) string {
	switch a := v.(type) {
	case nil:
		return ""
	case string:
		return a
	case json.Number:
		return a.String()
	case bool:
		if a {
			return "True" // Python bool repr; only reachable via odd schemas
		}
		return "False"
	default:
		return fmt.Sprint(a)
	}
}

// gemma4TypeUpper is `value['type'] | upper` (Undefined | upper -> "").
func gemma4TypeUpper(v any) string {
	return strings.ToUpper(gemma4Str(gemma4Lookup(v, "type")))
}

// gemma4QuoteJoin renders required-style lists: <|"|>item<|"|> joined by ','
// (tpl L36-L41, L72-L78, L96-L101).
func gemma4QuoteJoin(list []any) string {
	parts := make([]string, 0, len(list))
	for _, item := range list {
		parts = append(parts, gemma4StringDelim+gemma4Str(item)+gemma4StringDelim)
	}
	return strings.Join(parts, ",")
}

// formatGemma4FunctionDeclaration is the format_function_declaration macro,
// tpl L86-L117: declaration:name{description:<|"|>..<|"|>[,parameters:{..}]
// [,response:{..}]}. Brace placement (incl. the parameters block being closed
// by the type clause) is replicated exactly, quirks and all.
func formatGemma4FunctionDeclaration(tool map[string]any) string {
	fn, _ := tool["function"].(map[string]any)
	var b strings.Builder
	b.WriteString("declaration:" + gemma4Str(gemma4Lookup(fn, "name")))
	b.WriteString("{description:" + gemma4StringDelim + gemma4Str(gemma4Lookup(fn, "description")) + gemma4StringDelim)
	params := gemma4Lookup(fn, "parameters")
	if gemma4Truthy(params) { // tpl L89
		b.WriteString(",parameters:{")
		if props, ok := gemma4Lookup(params, "properties").(map[string]any); ok && gemma4Truthy(gemma4Lookup(params, "properties")) { // tpl L92
			required, _ := gemma4Lookup(params, "required").([]any)
			b.WriteString("properties:{" + formatGemma4Parameters(props, required, false) + "},")
		}
		if required, ok := gemma4Lookup(params, "required").([]any); ok && len(required) > 0 { // tpl L95
			b.WriteString("required:[" + gemma4QuoteJoin(required) + "],")
		}
		if gemma4Truthy(gemma4Lookup(params, "type")) { // tpl L102: closes the parameters block
			b.WriteString("type:" + gemma4StringDelim + gemma4TypeUpper(params) + gemma4StringDelim + "}")
		}
	}
	if fn != nil { // tpl L106: `'response' in tool_data['function']`
		if resp, present := fn["response"]; present {
			b.WriteString(",response:{")
			if gemma4Truthy(gemma4Lookup(resp, "description")) {
				b.WriteString("description:" + gemma4StringDelim + gemma4Str(gemma4Lookup(resp, "description")) + gemma4StringDelim + ",")
			}
			if gemma4TypeUpper(resp) == "OBJECT" { // tpl L112: closes the response block
				b.WriteString("type:" + gemma4StringDelim + gemma4TypeUpper(resp) + gemma4StringDelim + "}")
			}
		}
	}
	b.WriteString("}")
	return b.String()
}

// formatGemma4Parameters is the format_parameters macro, tpl L1-L85. Each
// property renders as key:{[description][,enum|items][,nullable][,properties]
// [,required],type:<|"|>TYPE<|"|>} with the comma threading of the macro's
// add_comma flag.
func formatGemma4Parameters(properties map[string]any, required []any, filterKeys bool) string {
	_ = required                     // tpl L1: passed through by callers but never read here
	standardKeys := map[string]bool{ // tpl L2
		"description": true, "type": true, "properties": true, "required": true, "nullable": true,
	}
	var b strings.Builder
	foundFirst := false
	for _, key := range gemma4DictsortKeys(properties) {
		if filterKeys && standardKeys[key] { // tpl L6
			continue
		}
		value := properties[key]
		if foundFirst {
			b.WriteString(",")
		}
		foundFirst = true
		b.WriteString(key + ":{") // tpl L9
		addComma := false
		comma := func() {
			if addComma {
				b.WriteString(",")
			} else {
				addComma = true
			}
		}
		typeUpper := gemma4TypeUpper(value)

		if gemma4Truthy(gemma4Lookup(value, "description")) { // tpl L10-L13
			b.WriteString("description:" + gemma4StringDelim + gemma4Str(gemma4Lookup(value, "description")) + gemma4StringDelim)
			addComma = true
		}
		switch typeUpper {
		case "STRING": // tpl L14-L19
			if enum := gemma4Lookup(value, "enum"); gemma4Truthy(enum) {
				comma()
				b.WriteString("enum:" + formatGemma4Argument(enum, true))
			}
		case "ARRAY": // tpl L20-L55
			if items, ok := gemma4Lookup(value, "items").(map[string]any); ok && len(items) > 0 {
				comma()
				b.WriteString("items:{")
				itemsFound := false
				for _, itemKey := range gemma4DictsortKeys(items) {
					itemValue := items[itemKey]
					if itemValue == nil { // tpl L25: `is not none`
						continue
					}
					if itemsFound {
						b.WriteString(",")
					}
					itemsFound = true
					switch itemKey {
					case "properties": // tpl L29-L34
						b.WriteString("properties:{")
						if m, ok := itemValue.(map[string]any); ok {
							itemsRequired, _ := items["required"].([]any)
							b.WriteString(formatGemma4Parameters(m, itemsRequired, false))
						}
						b.WriteString("}")
					case "required": // tpl L35-L41
						list, _ := itemValue.([]any)
						b.WriteString("required:[" + gemma4QuoteJoin(list) + "]")
					case "type": // tpl L42-L47
						if s, ok := itemValue.(string); ok {
							b.WriteString("type:" + formatGemma4Argument(strings.ToUpper(s), true))
						} else if list, ok := itemValue.([]any); ok {
							upped := make([]any, len(list))
							for li, lv := range list {
								upped[li] = strings.ToUpper(gemma4Str(lv))
							}
							b.WriteString("type:" + formatGemma4Argument(upped, true))
						}
					default: // tpl L48-L49
						b.WriteString(itemKey + ":" + formatGemma4Argument(itemValue, true))
					}
				}
				b.WriteString("}")
			}
		}
		if gemma4Truthy(gemma4Lookup(value, "nullable")) { // tpl L56-L59
			comma()
			b.WriteString("nullable:true")
		}
		if typeUpper == "OBJECT" { // tpl L60-L80
			if props, ok := gemma4Lookup(value, "properties").(map[string]any); ok { // tpl L61: defined and mapping
				comma()
				req, _ := gemma4Lookup(value, "required").([]any)
				b.WriteString("properties:{" + formatGemma4Parameters(props, req, false) + "}")
			} else if vm, ok := value.(map[string]any); ok { // tpl L66
				comma()
				req, _ := gemma4Lookup(value, "required").([]any)
				b.WriteString("properties:{" + formatGemma4Parameters(vm, req, true) + "}")
			}
			if req, ok := gemma4Lookup(value, "required").([]any); ok && len(req) > 0 { // tpl L72
				comma()
				b.WriteString("required:[" + gemma4QuoteJoin(req) + "]")
			}
		}
		comma() // tpl L81-L82: type is always last and closes the property
		b.WriteString("type:" + gemma4StringDelim + typeUpper + gemma4StringDelim + "}")
	}
	return b.String()
}

// formatGemma4ToolResponseBlock is the format_tool_response_block macro,
// tpl L160-L173, restricted to the string-response branch: pb tool messages
// carry flattened string content, so the mapping branch is unreachable.
func formatGemma4ToolResponseBlock(toolName, response string) string {
	return gemma4ToolResponseOpen +
		"response:" + toolName + "{value:" + formatGemma4Argument(response, false) + "}" +
		gemma4ToolResponseClose
}

// stripGemma4Thinking is the strip_thinking macro, tpl L148-L158: split on
// <channel|>, drop everything from <|channel> onward in each part, trim.
func stripGemma4Thinking(text string) string {
	var b strings.Builder
	for _, part := range strings.Split(text, gemma4ChannelClose) {
		if idx := strings.Index(part, gemma4ChannelOpen); idx >= 0 {
			b.WriteString(part[:idx])
		} else {
			b.WriteString(part)
		}
	}
	return strings.TrimSpace(b.String())
}
