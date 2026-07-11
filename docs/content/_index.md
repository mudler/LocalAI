+++
title = "LocalAI"
description = "The open, modular AI runtime. Run text, vision, voice, image, video, agents, and more on hardware you control."
type = "home"
+++

<div class="lai-home">
  <section class="lai-hero">
    <div class="lai-hero__copy">
      <p class="lai-signal"><span></span> Open source · MIT licensed</p>
      <h1>One runtime.<br><strong>Every kind of AI.</strong><br>Your hardware.</h1>
      <p class="lai-hero__lede">LocalAI runs text, vision, speech, sound, images, video, embeddings, reranking, and autonomous agents behind one modular stack—from a CPU laptop to a distributed GPU cluster.</p>
      <div class="lai-actions">
        <a class="lai-button" href="/installation/">Install LocalAI <b>→</b></a>
        <a class="lai-link" href="https://github.com/mudler/LocalAI">View on GitHub ↗</a>
      </div>
      <div class="lai-proof"><span>60+ backends</span><span>CPU to cluster</span><span>OpenAI · Anthropic · Ollama · ElevenLabs APIs</span></div>
    </div>
  </section>

  <section class="lai-breadth">
    <header><p>The runtime, not just the endpoint.</p><h2>Bring the model. Choose the engine. Keep control.</h2></header>
    <div class="lai-lanes">
      <a href="/features/text-generation/"><span>Reason</span><b>Language models · tools · structured output</b><em>Text</em></a>
      <a href="/features/openai-realtime/"><span>Listen & speak</span><b>Realtime WebRTC · transcription · TTS · diarization</b><em>Voice</em></a>
      <a href="/features/image-generation/"><span>Create</span><b>Images · video · music · sound</b><em>Media</em></a>
      <a href="/features/object-detection/"><span>See</span><b>Vision · detection · recognition · depth</b><em>Perception</em></a>
      <a href="/features/agents/"><span>Act</span><b>Agents · MCP · skills · RAG · interactive tools</b><em>Agentic</em></a>
    </div>
  </section>

  <section class="lai-architecture">
    <div class="lai-architecture__copy">
      <p>A small core, not a giant bundle.</p>
      <h2>Backends arrive when the model needs them.</h2>
      <p>LocalAI keeps the core lean. Each backend wraps a best-in-class engine—llama.cpp, vLLM, SGLang, MLX, whisper.cpp, diffusion engines, and many more—as an isolated service pulled on demand.</p>
      <ul><li>Install, update, or remove engines independently.</li><li>Mix CPU, NVIDIA, AMD, Intel, Apple Silicon, Vulkan, and Jetson.</li><li>Build your own backend in any language through an open gRPC contract.</li></ul>
      <a href="/reference/architecture/">Explore the architecture →</a>
    </div>
    <figure><img src="/images/diagrams/composable-core.png" alt="LocalAI's small core connected to independent on-demand model backends" /></figure>
  </section>

  <section class="lai-engines">
    <div class="lai-engines__intro">
      <p>We integrate the best engines. We build new ones, too.</p>
      <h2>Inference work that moves the open ecosystem forward.</h2>
      <p>The LocalAI team develops native C, C++, Rust, and GGML engines when the available stack is too heavy, too closed, or simply does not exist yet.</p>
      <a href="https://github.com/mudler/LocalAI#backends-built-by-us">See the engines we maintain ↗</a>
    </div>
    <div class="lai-engine-reel">
      <div><span>Speech</span><b>parakeet.cpp</b><small>Streaming multilingual ASR</small></div>
      <div><span>Voice</span><b>vibevoice.cpp</b><small>Long-form TTS and ASR</small></div>
      <div><span>Identity</span><b>voice-detect.cpp</b><small>Speaker recognition and analysis</small></div>
      <div><span>Vision</span><b>face-detect.cpp</b><small>Recognition and anti-spoofing</small></div>
      <div><span>Perception</span><b>locate-anything.cpp</b><small>Open-vocabulary detection</small></div>
      <div><span>Privacy</span><b>privacy-filter.cpp</b><small>Native PII detection</small></div>
      <div><span>3D</span><b>free-splatter.cpp</b><small>Pose-free reconstruction</small></div>
      <div><span>Quantization</span><b>apex-quant</b><small>MoE-aware GGUF recipes</small></div>
    </div>
  </section>

  <section class="lai-scale">
    <header><p>Start on one machine. Keep going.</p><h2>The same runtime from workstation to private AI fabric.</h2></header>
    <div class="lai-scale__path">
      <div><span>01</span><b>Laptop</b><p>Run useful models locally, including CPU-only setups.</p></div>
      <div><span>02</span><b>Team server</b><p>Add authentication, API keys, roles, quotas, and usage visibility.</p></div>
      <div><span>03</span><b>Distributed cluster</b><p>Route across workers, fit models across devices, and scale with demand.</p></div>
    </div>
  </section>

  <section class="lai-platform">
    <div><p>More than inference</p><h2>A complete local AI control plane.</h2></div>
    <div class="lai-platform__list">
      <article><b>Agents built in</b><p>Create agents with MCP tools, skills, memory, RAG, citations, and streamed execution from the UI or API.</p></article>
      <article><b>Realtime by design</b><p>Build interruptible voice experiences with WebRTC, streaming STT, LLM output, and TTS.</p></article>
      <article><b>Privacy you can enforce</b><p>Keep data on your infrastructure and add PII analysis, redaction, policy middleware, and audit visibility.</p></article>
      <article><b>Models under your control</b><p>Discover capabilities, import models, fine-tune, quantize, route, and monitor them in one place.</p></article>
    </div>
  </section>

  <section class="lai-start">
    <div><p>One command to begin</p><h2>Run your first local AI stack.</h2></div>
    <pre><code>docker run -ti --name local-ai -p 8080:8080 localai/localai:latest</code></pre>
    <div class="lai-start__links"><a href="/installation/">Installation options</a><a href="https://models.localai.io">Browse models</a><a href="/model-compatibility/">Compare backends</a><a href="https://discord.gg/uJAeKSAGDy">Join Discord</a></div>
  </section>
</div>
