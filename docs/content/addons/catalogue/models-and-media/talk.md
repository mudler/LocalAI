+++
disableToc = true
title = "Talk"
description = "Have a spoken conversation with a model: your voice goes in, the answer is spoken back."
weight = 20
keywords = ["talk", "voice", "realtime", "add-on"]
+++

The Talk add-on (ID `talk`) is a page for a spoken conversation with a model. It is separate from Studio. In the current development builds, it is in the development channel of the add-on library, so it can change.

## At a glance

| | |
|---|---|
| What it adds | The page `/app/addons/talk/talk`. It opens a WebRTC session to a realtime pipeline through `/v1/realtime/calls` and reads the pipeline list from `/api/pipeline-models`, with your own session. It uses the MCP servers that you added in the browser. |
| Who installs it | An administrator, from **Add-ons**, **Find add-ons**, with **Install and turn on**. With authentication off, you are the administrator. |
| Who turns it on | A person needs the `addons` feature. It is off by default for new users on a server with accounts. The core keeps the two routes and checks every call. |
| Guide | [Realtime API]({{% relref "/api/openai-realtime" %}}) and [The Talk add-on in the contract reference]({{% relref "/addons/contract-reference#the-talk-add-on" %}}) |
