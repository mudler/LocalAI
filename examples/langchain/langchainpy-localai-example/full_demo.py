import os
from langchain.chat_models import ChatOpenAI
from langchain import PromptTemplate, LLMChain
from langchain.prompts.chat import (
    ChatPromptTemplate,
    SystemMessagePromptTemplate,
    AIMessagePromptTemplate,
    HumanMessagePromptTemplate,
)
from langchain.schema import (
    AIMessage,
    HumanMessage,
    SystemMessage
)

print('Langchain + LocalAI PYTHON Tests')

base_path = os.environ.get('OPENAI_API_BASE', 'http://api:8080/v1')
key = os.environ.get('OPENAI_API_KEY', '-')
model_name = os.environ.get('MODEL_NAME', 'gpt-3.5-turbo')


chat = ChatOpenAI(temperature=0, openai_api_base=base_path, openai_api_key=key, model_name=model_name, max_tokens=100)

print("Created ChatOpenAI for ", chat.model_name)

template = "You are a helpful assistant that translates {input_language} to {output_language}."
system_message_prompt = SystemMessagePromptTemplate.from_template(template)
human_template = "{text}"
human_message_prompt = HumanMessagePromptTemplate.from_template(human_template)

chat_prompt = ChatPromptTemplate.from_messages([system_message_prompt, human_message_prompt])

print("ABOUT to execute")

# get a chat completion from the formatted messages
chat(chat_prompt.format_prompt(input_language="English", output_language="French", text="I love programming.").to_messages())

print(".");