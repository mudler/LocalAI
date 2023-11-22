
+++
disableToc = false
title = "Spark"
weight = 2
+++

an LLM-powered autonomous agent platform

![AI Spark](https://cdn.discordapp.com/attachments/1138961497691799693/1162248501120475227/unnamed.jpg?ex=653b3f44&is=6528ca44&hm=e0b2f74f2ba38097b8e78b80182e2e6beff4a3827847ddffb2693178a3dd6e05&)

A framework for autonomous agents who can work together to accomplish tasks using [LocalAI](https://github.com/go-skynet/LocalAI).

Github Link - https://github.com/cedriking/spark

## Setup

You will need at least Node 10.

[Download the repository](https://github.com/cedriking/spark), then install dependencies: `yarn` or `npm install`.

Rename the `.env.template` file at the root of the project to `.env` and add your secrets to it:

```
# the following are needed for the agent to be able to search the web:
GOOGLE_SEARCH_ENGINE_ID=... # create a custom search engine at https://cse.google.com/cse/all
GOOGLE_API_KEY=... # obtain from https://console.cloud.google.com/apis/credentials
AGENT_DELAY=... # optionally, a delay in milliseconds following every agent action
MODEL=... # any Llama.cpp LLM model
SERVER=... # optionally, a server to connect to (default http://localhost:8080)
```

You'll also need to enable the Google Custom Search API for your Google Cloud account, e.g. <https://console.cloud.google.com/apis/library/customsearch.googleapis.com>

## Running

Start the program:

```
yarn dev [# of agents]
```

or:

```
npm run dev [# of agents]
```

Interact with the agents through the console. Anything you type will be sent as a message to all agents currently.

## Action errors

After spinning up a new agent, you will often see them make some mistakes which generate errors:

- Trying to use an action before they've asked for `help` on it to know what its parameters are
- Trying to just use a raw text response instead of a correctly-formatted action (or raw text wrapping a code block which contains a valid action)
- Trying to use a multi-line parameter value without wrapping it in the multiline delimiter (`% ff9d7713-0bb0-40d4-823c-5a66de48761b`)

This is a normal period of adjustment as they learn to operate themselves. They generally will learn from these mistakes and recover, although agents sometimes devolve into endless error loops and can't figure out what the problem is. It's highly advised to never leave an agent unattended.

## Agent state

Each agent stores its state under the `.store` directory. Agent 1, for example, has

```
.store/1/memory
.store/1/goals
.store/1/notes
```

You can simply delete any of these things, or the whole agent folder (or the whole `.store`) to selectively wipe whatever state you want between runs. Otherwise, agents will pick up where you left off on restart.

A nice aspect of this is that when you want to debug a problem you ran into with a particular agent, you can delete the events in their memory subsequent to the point where the problem occurred, make changes to the code, and restart them to effectively replay that moment until you've fixed the bug. You can also ask an agent to implement a feature, and once they've done so you can restart, tell them that you've loaded the feature, and ask them to try it out.

Code based on [ai-legion](https://github.com/eumemic/ai-legion).
