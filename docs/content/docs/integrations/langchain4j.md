+++
disableToc = false
title = "LangChain4j"
description="LangChain for Java: Supercharge your Java application with the power of LLMs"
weight = 2
+++

Github: https://github.com/langchain4j/langchain4j

[![](https://img.shields.io/twitter/follow/langchain4j)](https://twitter.com/intent/follow?screen_name=langchain4j)
[![](https://dcbadge.vercel.app/api/server/JzTFvyjG6R?compact=true&style=flat)](https://discord.gg/JzTFvyjG6R)

## Project goals

The goal of this project is to simplify the integration of AI/LLM capabilities into your Java application.

This can be achieved thanks to:

- **A simple and coherent layer of abstractions**, designed to ensure that your code does not depend on concrete implementations such as LLM providers, embedding store providers, etc. This allows for easy swapping of components.
- **Numerous implementations of the above-mentioned abstractions**, providing you with a variety of LLMs and embedding stores to choose from.
- **Range of in-demand features on top of LLMs, such as:**
    - The capability to **ingest your own data** (documentation, codebase, etc.), allowing the LLM to act and respond based on your data.
    - **Autonomous agents** for delegating tasks (defined on the fly) to the LLM, which will strive to complete them.
    - **Prompt templates** to help you achieve the highest possible quality of LLM responses.
    - **Memory** to provide context to the LLM for your current and past conversations.
    - **Structured outputs** for receiving responses from the LLM with a desired structure as Java POJOs.
    - **"AI Services"** for declaratively defining complex AI behavior behind a simple API.
    - **Chains** to reduce the need for extensive boilerplate code in common use-cases.
    - **Auto-moderation** to ensure that all inputs and outputs to/from the LLM are not harmful.

## News

12 November:
- Integration with [OpenSearch](https://opensearch.org/) by [@riferrei](https://github.com/riferrei)
- Add support for loading documents from S3 by [@jmgang](https://github.com/jmgang)
- Integration with [PGVector](https://github.com/pgvector/pgvector) by [@kevin-wu-os](https://github.com/kevin-wu-os)
- Integration with [Ollama](https://ollama.ai/) by  [@Martin7-1](https://github.com/Martin7-1)
- Integration with [Amazon Bedrock](https://aws.amazon.com/bedrock/) by [@pascalconfluent](https://github.com/pascalconfluent)
- Adding Memory Id to Tool Method Call by [@benedictstrube](https://github.com/benedictstrube)
- [And more](https://github.com/langchain4j/langchain4j/releases/tag/0.24.0)

29 September:
- Updates to models API: return `Response<T>` instead of `T`. `Response<T>` contains token usage and finish reason.
- All model and embedding store integrations now live in their own modules
- Integration with [Vespa](https://vespa.ai/) by [@Heezer](https://github.com/Heezer)
- Integration with [Elasticsearch](https://www.elastic.co/) by [@Martin7-1](https://github.com/Martin7-1)
- Integration with [Redis](https://redis.io/) by [@Martin7-1](https://github.com/Martin7-1)
- Integration with [Milvus](https://milvus.io/) by [@IuriiKoval](https://github.com/IuriiKoval)
- Integration with [Astra DB](https://www.datastax.com/products/datastax-astra) and [Cassandra](https://cassandra.apache.org/) by [@clun](https://github.com/clun)
- Added support for overlap in document splitters
- Some bugfixes and smaller improvements 

29 August:
- Offline [text classification with embeddings](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/embedding/classification/EmbeddingModelTextClassifierExample.java)
- Integration with [Google Vertex AI](https://cloud.google.com/vertex-ai) by [@kuraleta](https://github.com/kuraleta)
- Reworked [document splitters](https://github.com/langchain4j/langchain4j/blob/main/langchain4j/src/main/java/dev/langchain4j/data/document/splitter/DocumentSplitters.java)
- In-memory embedding store can now be easily persisted
- [And more](https://github.com/langchain4j/langchain4j/releases/tag/0.22.0)

19 August:
- Integration with [Azure OpenAI](https://learn.microsoft.com/en-us/azure/ai-services/openai/overview) by [@kuraleta](https://github.com/kuraleta)
- Integration with Qwen models (DashScope) by [@jiangsier-xyz](https://github.com/jiangsier-xyz)
- [Integration with Chroma](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/embedding/store/ChromaEmbeddingStoreExample.java) by [@kuraleta](https://github.com/kuraleta)
- [Support for persistent ChatMemory](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/ServiceWithPersistentMemoryForEachUserExample.java)

10 August:
- [Integration with Weaviate](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/embedding/store/WeaviateEmbeddingStoreExample.java) by [@Heezer](https://github.com/Heezer)
- [Support for DOC, XLS and PPT document types](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/DocumentLoaderExamples.java) by [@oognuyh](https://github.com/oognuyh)
- [Separate chat memory for each user](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/ServiceWithMemoryForEachUserExample.java)
- [Custom in-process embedding models](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/embedding/model/InProcessEmbeddingModelExamples.java)
- Added lots of Javadoc
- [And more](https://github.com/langchain4j/langchain4j/releases/tag/0.19.0)

26 July:
- We've added integration with [LocalAI](https://localai.io/). Now, you can use LLMs hosted locally!
- Added support for [response streaming in AI Services](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/ServiceWithStreamingExample.java).

21 July:
- Now, you can do [text embedding inside your JVM](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/embedding/model/InProcessEmbeddingModelExamples.java).

17 July:
- You can now try out OpenAI's `gpt-3.5-turbo` and `text-embedding-ada-002` models with LangChain4j for free, without needing an OpenAI account and keys! Simply use the API key "demo".

15 July:
- Added EmbeddingStoreIngestor
- Redesigned document loaders (see FileSystemDocumentLoader)
- Simplified ConversationalRetrievalChain
- Renamed DocumentSegment into TextSegment
- Added output parsers for numeric types
- Added @UserName for AI Services
- Fixed [23](https://github.com/langchain4j/langchain4j/issues/23) and [24](https://github.com/langchain4j/langchain4j/issues/24)

11 July:

- Added ["Dynamic Tools"](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/ServiceWithDynamicToolsExample.java):
  Now, the LLM can generate code for tasks that require precise calculations, such as math and string manipulation. This will be dynamically executed in a style akin to GPT-4's code interpreter!
  We use [Judge0, hosted by Rapid API](https://rapidapi.com/judge0-official/api/judge0-ce/pricing), for code execution. You can subscribe and receive 50 free executions per day.

5 July:

- Now you can [add your custom knowledge base to "AI Services"](https://github.com/langchain4j/langchain4j-examples/blob/main/spring-boot-example/src/test/java/dev/example/CustomerSupportApplicationTest.java).
  Relevant information will be automatically retrieved and injected into the prompt. This way, the LLM will have a
  context of your data and will answer based on it!
- The current date and time can now be automatically injected into the prompt using
  special `{{current_date}}`, `{{current_time}}` and `{{current_date_time}}` placeholders.

3 July:

- Added support for Spring Boot 3

2 July:

- [Added Spring Boot Starter](https://github.com/langchain4j/langchain4j-examples/blob/main/spring-boot-example/src/test/java/dev/example/CustomerSupportApplicationTest.java)
- Added support for HuggingFace models

1 July:

- [Added "Tools"](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/ServiceWithToolsExample.java) (support for OpenAI functions)

## Highlights

You can declaratively define concise "AI Services" that are powered by LLMs:

```java
interface Assistant {

    String chat(String userMessage);
}

Assistant assistant = AiServices.create(Assistant.class, model);

String answer = assistant.chat("Hello");
    
System.out.println(answer);
// Hello! How can I assist you today?
```

You can use LLM as a classifier:

```java
enum Sentiment {
    POSITIVE, NEUTRAL, NEGATIVE
}

interface SentimentAnalyzer {

    @UserMessage("Analyze sentiment of {{it}}")
    Sentiment analyzeSentimentOf(String text);

    @UserMessage("Does {{it}} have a positive sentiment?")
    boolean isPositive(String text);
}

SentimentAnalyzer sentimentAnalyzer = AiServices.create(SentimentAnalyzer.class, model);

Sentiment sentiment = sentimentAnalyzer.analyzeSentimentOf("It is good!");
// POSITIVE

boolean positive = sentimentAnalyzer.isPositive("It is bad!");
// false
```

You can easily extract structured information from unstructured data:

```java
class Person {

    private String firstName;
    private String lastName;
    private LocalDate birthDate;

    public String toString() {...}
}

interface PersonExtractor {

    @UserMessage("Extract information about a person from {{it}}")
    Person extractPersonFrom(String text);
}

PersonExtractor extractor = AiServices.create(PersonExtractor.class, model);

String text = "In 1968, amidst the fading echoes of Independence Day, "
    + "a child named John arrived under the calm evening sky. "
    + "This newborn, bearing the surname Doe, marked the start of a new journey.";

Person person = extractor.extractPersonFrom(text);
// Person { firstName = "John", lastName = "Doe", birthDate = 1968-07-04 }
```

You can define more sophisticated prompt templates using mustache syntax:

```java
interface Translator {

    @SystemMessage("You are a professional translator into {{language}}")
    @UserMessage("Translate the following text: {{text}}")
    String translate(@V("text") String text, @V("language") String language);
}

Translator translator = AiServices.create(Translator.class, model);

String translation = translator.translate("Hello, how are you?", "Italian");
// Ciao, come stai?
```

You can provide tools that LLMs can use! Can be anything: retrieve information from DB, call APIs, etc.
See example [here](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/ServiceWithToolsExample.java).

## Compatibility

- Java: 8 or higher
- Spring Boot: 2 or 3

## Getting started

1. Add LangChain4j OpenAI dependency to your project:
    - Maven:
      ```
      <dependency>
          <groupId>dev.langchain4j</groupId>
          <artifactId>langchain4j-open-ai</artifactId>
          <version>0.24.0</version>
      </dependency>
      ```
    - Gradle:
      ```
      implementation 'dev.langchain4j:langchain4j-open-ai:0.24.0'
      ```

2. Import your OpenAI API key:
    ```java
    String apiKey = System.getenv("OPENAI_API_KEY");
    ```
    You can use the API key "demo" to test OpenAI, which we provide for free.
    [How to gen an API key?](https://github.com/langchain4j/langchain4j#how-to-get-an-api-key)


3. Create an instance of a model and start interacting:
    ```java
    OpenAiChatModel model = OpenAiChatModel.withApiKey(apiKey);
    
    String answer = model.generate("Hello world!");
    
    System.out.println(answer); // Hello! How can I assist you today?
    ```

## Disclaimer

Please note that the library is in active development and:

- Many features are still missing. We are working hard on implementing them ASAP.
- API might change at any moment. At this point, we prioritize good design in the future over backward compatibility
  now. We hope for your understanding.
- We need your input! Please [let us know](https://github.com/langchain4j/langchain4j/issues/new/choose) what features you need and your concerns about the current implementation.

## Current capabilities:

- AI Services:
    - [Simple](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/SimpleServiceExample.java)
    - [With Memory](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/ServiceWithMemoryExample.java)
    - [With Tools](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/ServiceWithToolsExample.java)
    - [With Streaming](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/ServiceWithStreamingExample.java)
    - [With Retriever](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/ServiceWithRetrieverExample.java)
    - [With Auto-Moderation](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/ServiceWithAutoModerationExample.java)
    - [With Structured Outputs, Structured Prompts, etc](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/OtherServiceExamples.java)
- Integration with [OpenAI](https://platform.openai.com/docs/introduction) and [Azure OpenAI](https://learn.microsoft.com/en-us/azure/ai-services/openai/overview) for:
    - [Chats](https://platform.openai.com/docs/guides/chat) (sync + streaming + functions)
    - [Completions](https://platform.openai.com/docs/guides/completion) (sync + streaming)
    - [Embeddings](https://platform.openai.com/docs/guides/embeddings)
- Integration with [Google Vertex AI](https://cloud.google.com/vertex-ai) for:
    - [Chats](https://cloud.google.com/vertex-ai/docs/generative-ai/chat/chat-prompts)
    - [Completions](https://cloud.google.com/vertex-ai/docs/generative-ai/text/text-overview)
    - [Embeddings](https://cloud.google.com/vertex-ai/docs/generative-ai/embeddings/get-text-embeddings)
- Integration with [HuggingFace Inference API](https://huggingface.co/docs/api-inference/index) for:
    - [Chats](https://huggingface.co/docs/api-inference/detailed_parameters#text-generation-task)
    - [Completions](https://huggingface.co/docs/api-inference/detailed_parameters#text-generation-task)
    - [Embeddings](https://huggingface.co/docs/api-inference/detailed_parameters#feature-extraction-task)
- Integration with [LocalAI](https://localai.io/) for:
  - Chats (sync + streaming + functions)
  - Completions (sync + streaming)
  - Embeddings
- Integration with [DashScope](https://dashscope.aliyun.com/) for:
    - Chats (sync + streaming)
    - Completions (sync + streaming)
    - Embeddings
- [Chat memory](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/ChatMemoryExamples.java)
- [Persistent chat memory](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/ServiceWithPersistentMemoryForEachUserExample.java)
- [Chat with Documents](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/ChatWithDocumentsExamples.java)
- Integration with [Astra DB](https://www.datastax.com/products/datastax-astra) and [Cassandra](https://cassandra.apache.org/)
- [Integration](https://github.com/langchain4j/langchain4j-examples/blob/main/chroma-example/src/main/java/ChromaEmbeddingStoreExample.java) with [Chroma](https://www.trychroma.com/)
- [Integration](https://github.com/langchain4j/langchain4j-examples/blob/main/elasticsearch-example/src/main/java/ElasticsearchEmbeddingStoreExample.java) with [Elasticsearch](https://www.elastic.co/)
- [Integration](https://github.com/langchain4j/langchain4j-examples/blob/main/milvus-example/src/main/java/MilvusEmbeddingStoreExample.java) with [Milvus](https://milvus.io/)
- [Integration](https://github.com/langchain4j/langchain4j-examples/blob/main/pinecone-example/src/main/java/PineconeEmbeddingStoreExample.java) with [Pinecone](https://www.pinecone.io/)
- [Integration](https://github.com/langchain4j/langchain4j-examples/blob/main/redis-example/src/main/java/RedisEmbeddingStoreExample.java) with [Redis](https://redis.io/)
- [Integration](https://github.com/langchain4j/langchain4j-examples/blob/main/vespa-example/src/main/java/VespaEmbeddingStoreExample.java) with [Vespa](https://vespa.ai/)
- [Integration](https://github.com/langchain4j/langchain4j-examples/blob/main/weaviate-example/src/main/java/WeaviateEmbeddingStoreExample.java) with [Weaviate](https://weaviate.io/)
- [In-memory embedding store](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/embedding/store/InMemoryEmbeddingStoreExample.java) (can be persisted)
- [Structured outputs](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/OtherServiceExamples.java)
- [Prompt templates](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/PromptTemplateExamples.java)
- [Structured prompt templates](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/StructuredPromptTemplateExamples.java)
- [Streaming of LLM responses](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/StreamingExamples.java)
- [Loading txt, html, pdf, doc, xls and ppt documents from the file system and via URL](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/DocumentLoaderExamples.java)
- [Splitting documents into segments](https://github.com/langchain4j/langchain4j-examples/blob/main/other-examples/src/main/java/ChatWithDocumentsExamples.java):
    - by paragraphs, lines, sentences, words, etc
    - recursively
    - with overlap
- Token count estimation (so that you can predict how much you will pay)

## Coming soon:

- Extending "AI Service" features
- Integration with more LLM providers (commercial and free)
- Integrations with more embedding stores (commercial and free)
- Support for more document types
- Long-term memory for chatbots and agents
- Chain-of-Thought and Tree-of-Thought

## Request features

Please [let us know](https://github.com/langchain4j/langchain4j/issues/new/choose) what features you need!

## Contribute

Please help us make this open-source library better by contributing.

Some guidelines:
1. Follow [Google's Best Practices for Java Libraries](https://jlbp.dev/).
2. Keep the code compatible with Java 8.
3. Avoid adding new dependencies as much as possible. If absolutely necessary, try to (re)use the same libraries which are already present.
4. Follow existing code styles present in the project.
5. Ensure to add Javadoc where necessary.
6. Provide unit and/or integration tests for your code.
7. Large features should be discussed with maintainers before implementation.

## Use cases

You might ask why would I need all of this?
Here are a couple of examples:

- You want to implement a custom AI-powered chatbot that has access to your data and behaves the way you want it:
    - Customer support chatbot that can:
        - politely answer customer questions
        - take /change/cancel orders
    - Educational assistant that can:
        - Teach various subjects
        - Explain unclear parts
        - Assess user's understanding/knowledge
- You want to process a lot of unstructured data (files, web pages, etc) and extract structured information from them.
  For example:
    - extract insights from customer reviews and support chat history
    - extract interesting information from the websites of your competitors
    - extract insights from CVs of job applicants
- You want to generate information, for example:
    - Emails tailored for each of your customers
    - Content for your app/website:
        - Blog posts
        - Stories
- You want to transform information, for example:
    - Summarize
    - Proofread and rewrite
    - Translate

## Best practices

We highly recommend
watching [this amazing 90-minute tutorial](https://www.deeplearning.ai/short-courses/chatgpt-prompt-engineering-for-developers/)
on prompt engineering best practices, presented by Andrew Ng (DeepLearning.AI) and Isa Fulford (OpenAI).
This course will teach you how to use LLMs efficiently and achieve the best possible results. Good investment of your
time!

Here are some best practices for using LLMs:

- Be responsible. Use AI for Good.
- Be specific. The more specific your query, the best results you will get.
- Add a ["Let’s think step by step" instruction](https://arxiv.org/pdf/2205.11916.pdf) to your prompt.
- Specify steps to achieve the desired goal yourself. This will make the LLM do what you want it to do.
- Provide examples. Sometimes it is best to show LLM a few examples of what you want instead of trying to explain it.
- Ask LLM to provide structured output (JSON, XML, etc). This way you can parse response more easily and distinguish
  different parts of it.
- Use unusual delimiters, such as \```triple backticks``` to help the LLM distinguish
  data or input from instructions.

## How to get an API key
You will need an API key from OpenAI (paid) or HuggingFace (free) to use LLMs hosted by them.

We recommend using OpenAI LLMs (`gpt-3.5-turbo` and `gpt-4`) as they are by far the most capable and are reasonably priced.

It will cost approximately $0.01 to generate 10 pages (A4 format) of text with `gpt-3.5-turbo`. With `gpt-4`, the cost will be $0.30 to generate the same amount of text. However, for some use cases, this higher cost may be justified.

[How to get OpenAI API key](https://www.howtogeek.com/885918/how-to-get-an-openai-api-key/).

For embeddings, we recommend using one of the models from the [HuggingFace MTEB leaderboard](https://huggingface.co/spaces/mteb/leaderboard).
You'll have to find the best one for your specific use case.

Here's how to get a HuggingFace API key:
- Create an account on https://huggingface.co
- Go to https://huggingface.co/settings/tokens
- Generate a new access token
