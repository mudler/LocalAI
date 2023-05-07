## This is a fork/based from https://gist.github.com/wiseman/4a706428eaabf4af1002a07a114f61d6

from io import StringIO
import sys
import os
from typing import Dict, Optional

from langchain.agents import load_tools
from langchain.agents import initialize_agent
from langchain.agents.tools import Tool
from langchain.llms import OpenAI

base_path = os.environ.get('OPENAI_API_BASE', 'http://localhost:8080/v1')
model_name = os.environ.get('MODEL_NAME', 'gpt-3.5-turbo')

class PythonREPL:
    """Simulates a standalone Python REPL."""

    def __init__(self):
        pass        

    def run(self, command: str) -> str:
        """Run command and returns anything printed."""
        old_stdout = sys.stdout
        sys.stdout = mystdout = StringIO()
        try:
            exec(command, globals())
            sys.stdout = old_stdout
            output = mystdout.getvalue()
        except Exception as e:
            sys.stdout = old_stdout
            output = str(e)
        return output

llm = OpenAI(temperature=0.0, openai_api_base=base_path, model_name=model_name)
python_repl = Tool(
        "Python REPL",
        PythonREPL().run,
        """A Python shell. Use this to execute python commands. Input should be a valid python command.
        If you expect output it should be printed out.""",
    )
tools = [python_repl]
agent = initialize_agent(tools, llm, agent="zero-shot-react-description", verbose=True)
agent.run("What is the 10th fibonacci number?")