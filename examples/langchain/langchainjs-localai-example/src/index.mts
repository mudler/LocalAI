import { loadQAStuffChain } from "langchain/chains";
import { Document } from "langchain/document";
import { pull } from "langchain/hub";
import { AgentExecutor, createOpenAIToolsAgent } from "langchain/agents";
import {Calculator} from "@langchain/community/tools/calculator";
import { ChatOpenAI } from "@langchain/openai";
import type { ChatPromptTemplate } from "@langchain/core/prompts";

const pathToLocalAI = process.env['OPENAI_API_BASE'] || 'http://api:8080/v1';
const fakeApiKey = process.env['OPENAI_API_KEY'] || '-';
const modelName = process.env['MODEL_NAME'] || 'gpt-3.5-turbo';

function getModel(): ChatOpenAI {
  return new ChatOpenAI({
    prefixMessages: [
      {
        role: "system",
        content: "You are a helpful assistant that answers in pirate language",
      },
    ],
    modelName: modelName,
    maxTokens: 50,
    openAIApiKey: fakeApiKey,
    maxRetries: 2
  }, {
    basePath: pathToLocalAI,
    apiKey: fakeApiKey,
  });
}

// Minimal example.
export const run = async () => {
  const model = getModel();
  console.log(`about to model.invoke at ${new Date().toUTCString()}`);
  const res = await model.invoke(
    "What would be a good company name a company that makes colorful socks?"
  );
  console.log(`${new Date().toUTCString()}`);
  console.log({ res });
};

await run();

// This example uses the `StuffDocumentsChain`
export const run2 = async () => {
  const model = getModel();
  const chainA = loadQAStuffChain(model);
  const docs = [
    new Document({ pageContent: "Harrison went to Harvard." }),
    new Document({ pageContent: "Ankush went to Princeton." }),
  ];
  const resA = await chainA.invoke({
    input_documents: docs,
    question: "Where did Harrison go to college?",
  });
  console.log({ resA });
};

await run2();

// Quickly thrown together example of using tools + agents.
// This seems like it should work, but it doesn't yet.
export const toolAgentTest = async () => {
  const model = getModel();

  const prompt = await pull<ChatPromptTemplate>("hwchase17/openai-tools-agent");

  const tools = [new Calculator()];

  const agent = await createOpenAIToolsAgent({
    llm: model,
    tools: tools,
    prompt: prompt
  });

  console.log("Loaded agent.");

  const agentExecutor = new AgentExecutor({
    agent,
    tools,
  });

  const input = `What is the value of (500 *2) + 350 - 13?`;

  console.log(`Executing with input "${input}"...`);

  const result = await agentExecutor.invoke({ input });

  console.log(`Got output ${result.output}`);
}

await toolAgentTest();
