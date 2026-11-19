+++
disableToc = true
title = "LocalAI documentation"
linkTitle = "LocalAI docs"
description = "Documentation for LocalAI: install it, run models of every kind behind an API in the OpenAI and Anthropic formats, build agents, and run it for a team."
type = "home"
keywords = ["documentation", "install", "quickstart", "team", "add-on", "sandbox", "office", "agents"]
+++

<p class="lai-lede">Run open models for chat, images, speech and video on your own machines. The API speaks the OpenAI and Anthropic formats. Your team gets chats, files and documents on top, and the assistant can write and run code.</p>

## Start in three steps

<div class="lai-steps">
<div class="lai-step">

<span class="lai-n">1</span>

### Install

The script works on Linux and on Macs with Apple Silicon. You can also use a container or Kubernetes. The desktop app for macOS and Linux is in the 5.0 release. See the [install guide]({{% relref "/start/install" %}}).

```bash
curl -sSL https://localai.io/install.sh | sh
```

</div>
<div class="lai-step">

<span class="lai-n">2</span>

### Run a first model

Start the server with `local-ai` and open the web interface. It suggests a model that fits your hardware and downloads it when you agree. The [quickstart]({{% relref "/start/quickstart" %}}) has the steps.

</div>
<div class="lai-step">

<span class="lai-n">3</span>

### Build an agent

In the web interface, go to **Agents**, choose **New agent** and give it instructions. See [Build your first agent]({{% relref "/start/first-agent" %}}).

</div>
</div>

## What you can do

<p class="lai-legend"><span class="lai-tag lai-tag-built">Built in</span> is part of LocalAI. <span class="lai-tag lai-tag-addon">Add-on</span> comes from the add-on library, and an administrator installs it. <span class="lai-tag lai-tag-admin">Admin on</span> is built in, but on a server with accounts an administrator must switch it on. With authentication off, you are the administrator. <span class="lai-tag lai-tag-admin">Next release</span> is in the development builds only.</p>

<div class="lai-feats">
<a class="lai-feat" href="{{% relref "/models/capabilities" %}}"><span class="lai-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 7l8-4 8 4-8 4zM4 12l8 4 8-4M4 17l8 4 8-4"/></svg></span><span class="lai-ft"><b>Every kind of model</b><span>Text, vision, images, video, music, speech, embeddings and reranking.</span></span><span class="lai-tag lai-tag-built">Built in</span></a>
<a class="lai-feat" href="{{% relref "/features/chats-and-agents/projects" %}}"><span class="lai-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 7h6l2 2h10v10H3z"/><path d="M12 12v4M10 14h4"/></svg></span><span class="lai-ft"><b>Projects</b><span>Chats, files, notes and agents for one piece of work.</span></span><span class="lai-tag lai-tag-admin">Next release</span></a>
<a class="lai-feat" href="{{% relref "/start/install/desktop" %}}"><span class="lai-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="4" width="18" height="12" rx="2"/><path d="M8 20h8M12 16v4"/></svg></span><span class="lai-ft"><b>Desktop app</b><span>Install and run LocalAI from a window on macOS and Linux.</span></span><span class="lai-tag lai-tag-admin">Next release</span></a>
<a class="lai-feat" href="{{% relref "/features/chats-and-agents/chats" %}}"><span class="lai-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 5h16v11H8l-4 4z"/></svg></span><span class="lai-ft"><b>Chats</b><span>Talk to a model. Files, sub-agents and tools work in the same chat.</span></span><span class="lai-tag lai-tag-built">Built in</span></a>
<a class="lai-feat" href="{{% relref "/features/chats-and-agents/chats-agents" %}}"><span class="lai-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="5" y="8" width="14" height="11" rx="2"/><path d="M12 4v4M9 13h.01M15 13h.01"/></svg></span><span class="lai-ft"><b>Saved agents</b><span>Instructions, tool grants, triggers and notifications.</span></span><span class="lai-tag lai-tag-admin">Admin on</span></a>
<a class="lai-feat" href="{{% relref "/features/files-and-documents/files" %}}"><span class="lai-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M3 7h6l2 2h10v10H3z"/></svg></span><span class="lai-ft"><b>Files and sharing</b><span>Folders, shares, Trash and History with Undo.</span></span><span class="lai-tag lai-tag-built">Built in</span></a>
<a class="lai-feat" href="{{% relref "/features/files-and-documents/collections" %}}"><span class="lai-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="11" cy="11" r="6"/><path d="M20 20l-4-4"/></svg></span><span class="lai-ft"><b>Knowledge</b><span>Search your documents by meaning.</span></span><span class="lai-tag lai-tag-admin">Admin on</span></a>
<a class="lai-feat" href="{{% relref "/addons/catalogue/work/sandbox" %}}"><span class="lai-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 9l3 3-3 3M13 15h4"/></svg></span><span class="lai-ft"><b>Sandbox</b><span>Run Python and shell in an isolated runtime. The network is off by default.</span></span><span class="lai-tag lai-tag-addon">Add-on</span></a>
<a class="lai-feat" href="{{% relref "/features/files-and-documents/office" %}}"><span class="lai-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><path d="M14 3v5h5"/></svg></span><span class="lai-ft"><b>Office documents</b><span>Word, Excel, PowerPoint and PDF files.</span></span><span class="lai-tag lai-tag-addon">Add-on</span></a>
<a class="lai-feat" href="{{% relref "/features/pages-and-home/home-widgets" %}}"><span class="lai-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="3" width="8" height="8" rx="2"/><rect x="13" y="3" width="8" height="18" rx="2"/><rect x="3" y="13" width="8" height="8" rx="2"/></svg></span><span class="lai-ft"><b>Home, pages and triggers</b><span>Widgets, live pages and schedules.</span></span><span class="lai-tag lai-tag-admin">Admin on</span></a>
<a class="lai-feat" href="{{% relref "/features/chat-apps" %}}"><span class="lai-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 12a8 8 0 0 1-11.5 7.2L4 20l1-4.5A8 8 0 1 1 21 12z"/></svg></span><span class="lai-ft"><b>Chat apps</b><span>Talk to an agent from Slack, Discord or another chat app.</span></span><span class="lai-tag lai-tag-addon">Add-on</span></a>
<a class="lai-feat" href="{{% relref "/concepts/users-and-sharing" %}}"><span class="lai-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="9" cy="8" r="3"/><path d="M3 20c0-3 3-5 6-5s6 2 6 5M17 11a3 3 0 0 0 0-6M21 20c0-2.5-1.8-4.2-4-4.8"/></svg></span><span class="lai-ft"><b>Team and sharing</b><span>Give people accounts and decide which features each role can use.</span></span><span class="lai-tag lai-tag-admin">Admin on</span></a>
<a class="lai-feat" href="{{% relref "/models/capabilities/vision" %}}"><span class="lai-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="3" y="4" width="18" height="16" rx="2"/><circle cx="9" cy="10" r="2"/><path d="M21 16l-5-5-8 8"/></svg></span><span class="lai-ft"><b>Images and video</b><span>Make images and video, and ask a model about a picture.</span></span><span class="lai-tag lai-tag-built">Built in</span></a>
<a class="lai-feat" href="{{% relref "/models/capabilities/audio" %}}"><span class="lai-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 10v4M8 7v10M12 4v16M16 8v8M20 11v2"/></svg></span><span class="lai-ft"><b>Speech and transcription</b><span>Turn text into speech, and speech into text.</span></span><span class="lai-tag lai-tag-built">Built in</span></a>
<a class="lai-feat" href="{{% relref "/models" %}}"><span class="lai-ic"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 7l8-4 8 4-8 4zM4 12l8 4 8-4M4 17l8 4 8-4"/></svg></span><span class="lai-ft"><b>Models and backends</b><span>The model gallery picks the engine for your machine.</span></span><span class="lai-tag lai-tag-built">Built in</span></a>
</div>

{{< devbuild install >}}

## Go further

<div class="lai-more">
<a href="{{% relref "/api" %}}"><b>API</b><span>OpenAI and Anthropic compatible</span></a>
<a href="{{% relref "/features/chats-and-agents/mcp" %}}"><b>MCP</b><span>Tools for models and agents</span></a>
<a href="{{% relref "/administration/distributed-mode" %}}"><b>Distributed mode</b><span>Run several nodes against one database</span></a>
<a href="{{% relref "/addons/build-your-own" %}}"><b>Build an add-on</b><span>Tutorial and contract</span></a>
<a href="{{% relref "/reference" %}}"><b>Reference</b><span>Command line, binaries and the FAQ</span></a>
<a href="{{% relref "/contributing" %}}"><b>Contributing</b><span>Build from source and write docs</span></a>
</div>
