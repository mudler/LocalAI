# LocalAI Demonstration with Embeddings and Chainlit

This demonstration shows you how to use embeddings with existing data in `LocalAI`, and how to integrate it with Chainlit for an interactive querying experience. We are using the `llama_index` library to facilitate the embedding and querying processes, and `chainlit` to provide an interactive interface. The `Weaviate` client is used as the embedding source.

## Prerequisites

Before proceeding, make sure you have the following installed:
- Weaviate client
- LocalAI and its dependencies
- Chainlit and its dependencies

## Getting Started

1. Clone this repository:
2. Navigate to the project directory:
3. Run the example: `chainlit run main.py`

# Highlight on `llama_index` and `chainlit`

`llama_index` is the key library that facilitates the process of embedding and querying data in LocalAI. It provides a seamless interface to integrate various components, such as `WeaviateVectorStore`, `LocalAI`, `ServiceContext`, and more, for a smooth querying experience.

`chainlit` is used to provide an interactive interface for users to query the data and see the results in real-time. It integrates with llama_index to handle the querying process and display the results to the user.

In this example, `llama_index` is used to set up the `VectorStoreIndex` and `QueryEngine`, and `chainlit` is used to handle the user interactions with `LocalAI` and display the results.

