
from langchain.llms import OpenAI

llm = OpenAI(temperature=0.9,model_name="gpt-3.5-turbo")
text = "What would be a good company name for a company that makes colorful socks?"
print(llm(text))
