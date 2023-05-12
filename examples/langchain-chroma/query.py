
import os
from langchain.vectorstores import Chroma
from langchain.embeddings import OpenAIEmbeddings
from langchain.text_splitter import RecursiveCharacterTextSplitter,CharacterTextSplitter
from langchain.llms import OpenAI
from langchain.chains import VectorDBQA
from langchain.document_loaders import TextLoader

base_path = os.environ.get('OPENAI_API_BASE', 'http://localhost:8080/v1')

# Load and process the text
loader = TextLoader('state_of_the_union.txt')
documents = loader.load()

text_splitter = CharacterTextSplitter(chunk_size=300, chunk_overlap=70)
texts = text_splitter.split_documents(documents)

# Embed and store the texts
# Supplying a persist_directory will store the embeddings on disk
persist_directory = 'db'

embedding = OpenAIEmbeddings()

# Now we can load the persisted database from disk, and use it as normal. 
vectordb = Chroma(persist_directory=persist_directory, embedding_function=embedding)
qa = VectorDBQA.from_chain_type(llm=OpenAI(temperature=0, model_name="gpt-3.5-turbo", openai_api_base=base_path), chain_type="stuff", vectorstore=vectordb)

query = "What the president said about taxes ?"
print(qa.run(query))

