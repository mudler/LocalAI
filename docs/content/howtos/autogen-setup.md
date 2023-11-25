
+++
disableToc = false
title = "Easy Demo - AutoGen"
weight = 2
+++

This is just a short demo of setting up ``LocalAI`` with Autogen, this is based on you already having a model setup.

```python
import os
import openai
import autogen

openai.api_key = "sx-xxx"
OPENAI_API_KEY = "sx-xxx"
os.environ['OPENAI_API_KEY'] = OPENAI_API_KEY

config_list_json = [
    {
        "model": "gpt-3.5-turbo",
        "api_base": "http://[YOURLOCALAIIPHERE]:8080/v1",
        "api_type": "open_ai",
        "api_key": "NULL",
    }
]

print("models to use: ", [config_list_json[i]["model"] for i in range(len(config_list_json))])

llm_config = {"config_list": config_list_json, "seed": 42}
user_proxy = autogen.UserProxyAgent(
    name="Admin",
    system_message="A human admin. Interact with the planner to discuss the plan. Plan execution needs to be approved by this admin.",
    code_execution_config={
        "work_dir": "coding",
        "last_n_messages": 8,
        "use_docker": "python:3",
    },
    human_input_mode="ALWAYS",
    is_termination_msg=lambda x: x.get("content", "").rstrip().endswith("TERMINATE"),
)
engineer = autogen.AssistantAgent(
    name="Coder",
    llm_config=llm_config,
)
scientist = autogen.AssistantAgent(
    name="Scientist",
    llm_config=llm_config,
    system_message="""Scientist. You follow an approved plan. You are able to categorize papers after seeing their abstracts printed. You don't write code."""
)
planner = autogen.AssistantAgent(
    name="Planner",
    system_message='''Planner. Suggest a plan. Revise the plan based on feedback from admin and critic, until admin approval.
The plan may involve an engineer who can write code and a scientist who doesn't write code.
Explain the plan first. Be clear which step is performed by an engineer, and which step is performed by a scientist.
''',
    llm_config=llm_config,
)
executor = autogen.UserProxyAgent(
    name="Executor",
    system_message="Executor. Execute the code written by the engineer and report the result.",
    human_input_mode="NEVER",
    code_execution_config={
        "work_dir": "coding",
        "last_n_messages": 8,
        "use_docker": "python:3",
    }
)
critic = autogen.AssistantAgent(
    name="Critic",
    system_message="Critic. Double check plan, claims, code from other agents and provide feedback. Check whether the plan includes adding verifiable info such as source URL.",
    llm_config=llm_config,
)
groupchat = autogen.GroupChat(agents=[user_proxy, engineer, scientist, planner, executor, critic], messages=[], max_round=999)
manager = autogen.GroupChatManager(groupchat=groupchat, llm_config=llm_config)


#autogen.ChatCompletion.start_logging()

#text_input = input("Please enter request: ")
text_input = ("Change this to a task you would like the group chat to do or comment this out and uncomment the other line!")

#Uncomment one of these two chats based on what you would like to do

#user_proxy.initiate_chat(engineer, message=str(text_input))
#For a one on one chat use this one ^

#user_proxy.initiate_chat(manager, message=str(text_input))
#To setup a group chat use this one ^
```

