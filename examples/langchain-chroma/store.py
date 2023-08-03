
import os
from langchain.vectorstores import Chroma
from langchain.embeddings import OpenAIEmbeddings
from langchain.text_splitter import CharacterTextSplitter
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

embedding = OpenAIEmbeddings(model="text-embedding-ada-002", openai_api_base=base_path)
vectordb = Chroma.from_documents(documents=texts, embedding=embedding, persist_directory=persist_directory)

vectordb.persist()
vectordb = None
