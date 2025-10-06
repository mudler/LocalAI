/*

https://github.com/david-haerer/chatapi

MIT License

Copyright (c) 2023 David Härer
Copyright (c) 2024 Ettore Di Giacinto

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

*/

function toggleLoader(show) {
  const loader = document.getElementById('loader');
  const sendButton = document.getElementById('send-button');
  
  if (show) {
    loader.style.display = 'block';
    sendButton.style.display = 'none';
    document.getElementById("input").disabled = true;
  } else {
    document.getElementById("input").disabled = false;
    loader.style.display = 'none';
    sendButton.style.display = 'block';
  }
}

function processThinkingTags(content) {
  const thinkingRegex = /<thinking>(.*?)<\/thinking>|<think>(.*?)<\/think>/gs;
  const parts = content.split(thinkingRegex);
  
  let regularContent = "";
  let thinkingContent = "";
  
  for (let i = 0; i < parts.length; i++) {
    if (i % 3 === 0) {
      // Regular content
      regularContent += parts[i];
    } else if (i % 3 === 1) {
      // <thinking> content
      thinkingContent = parts[i];
    } else if (i % 3 === 2) {
      // <think> content
      thinkingContent = parts[i];
    }
  }
  
  return {
    regularContent: regularContent.trim(),
    thinkingContent: thinkingContent.trim()
  };
}

function submitSystemPrompt(event) {
  event.preventDefault();
  localStorage.setItem("system_prompt", document.getElementById("systemPrompt").value);
  document.getElementById("systemPrompt").blur();
}

function handleShutdownResponse(event, modelName) {
  // Check if the request was successful
  if (event.detail.successful) {
    // Show a success message (optional)
    console.log(`Model ${modelName} stopped successfully`);
    
    // Refresh the page to update the UI
    window.location.reload();
  } else {
    // Show an error message (optional)
    console.error(`Failed to stop model ${modelName}`);
    
    // You could also show a user-friendly error message here
    // For now, we'll still refresh to show the current state
    window.location.reload();
  }
}

var images = [];
var audios = [];
var fileContents = [];
var currentFileNames = [];

async function extractTextFromPDF(pdfData) {
  try {
    const pdf = await pdfjsLib.getDocument({ data: pdfData }).promise;
    let fullText = '';
    
    for (let i = 1; i <= pdf.numPages; i++) {
      const page = await pdf.getPage(i);
      const textContent = await page.getTextContent();
      const pageText = textContent.items.map(item => item.str).join(' ');
      fullText += pageText + '\n';
    }
    
    return fullText;
  } catch (error) {
    console.error('Error extracting text from PDF:', error);
    throw error;
  }
}

function readInputFile() {
  if (!this.files || !this.files.length) return;

  Array.from(this.files).forEach(file => {
    const FR = new FileReader();
    currentFileNames.push(file.name);
    const fileExtension = file.name.split('.').pop().toLowerCase();
    
    FR.addEventListener("load", async function(evt) {
      if (fileExtension === 'pdf') {
        try {
          const content = await extractTextFromPDF(evt.target.result);
          fileContents.push({ name: file.name, content: content });
        } catch (error) {
          console.error('Error processing PDF:', error);
          fileContents.push({ name: file.name, content: "Error processing PDF file" });
        }
      } else {
        // For text and markdown files
        fileContents.push({ name: file.name, content: evt.target.result });
      }
    });

    if (fileExtension === 'pdf') {
      FR.readAsArrayBuffer(file);
    } else {
      FR.readAsText(file);
    }
  });
}

function submitPrompt(event) {
  event.preventDefault();

  const input = document.getElementById("input").value;
  let fullInput = input;
  
  // If there are file contents, append them to the input for the LLM
  if (fileContents.length > 0) {
    fullInput += "\n\nFile contents:\n";
    fileContents.forEach(file => {
      fullInput += `\n--- ${file.name} ---\n${file.content}\n`;
    });
  }
  
  // Show file icons in chat if there are files
  let displayContent = input;
  if (currentFileNames.length > 0) {
    displayContent += "\n\n";
    currentFileNames.forEach(fileName => {
      displayContent += `<i class="fa-solid fa-file"></i> Attached file: ${fileName}\n`;
    });
  }
  
  // Add the message to the chat UI with just the icons
  Alpine.store("chat").add("user", displayContent, images, audios);
  
  // Update the last message in the store with the full content
  const history = Alpine.store("chat").history;
  if (history.length > 0) {
    history[history.length - 1].content = fullInput;
  }
  
  document.getElementById("input").value = "";
  const systemPrompt = localStorage.getItem("system_prompt");
  Alpine.nextTick(() => { document.getElementById('messages').scrollIntoView(false); });
  promptGPT(systemPrompt, fullInput);
  
  // Reset file contents and names after sending
  fileContents = [];
  currentFileNames = [];
}

function readInputImage() {
  if (!this.files || !this.files.length) return;

  Array.from(this.files).forEach(file => {
    const FR = new FileReader();

    FR.addEventListener("load", function(evt) {
      images.push(evt.target.result);
    });

    FR.readAsDataURL(file);
  });
}

function readInputAudio() {
  if (!this.files || !this.files.length) return;

  Array.from(this.files).forEach(file => {
    const FR = new FileReader();

    FR.addEventListener("load", function(evt) {
      audios.push(evt.target.result);
    });

    FR.readAsDataURL(file);
  });
}

async function promptGPT(systemPrompt, input) {
  const model = document.getElementById("chat-model").value;
  const mcpMode = Alpine.store("chat").mcpMode;
  toggleLoader(true);

  messages = Alpine.store("chat").messages();

  // if systemPrompt isn't empty, push it at the start of messages
  if (systemPrompt) {
    messages.unshift({
      role: "system",
      content: systemPrompt
    });
  }

  // loop all messages, and check if there are images or audios. If there are, we need to change the content field
  messages.forEach((message) => {
    if ((message.image && message.image.length > 0) || (message.audio && message.audio.length > 0)) {
      // The content field now becomes an array
      message.content = [
        {
          "type": "text",
          "text": message.content
        }
      ]
      
      if (message.image && message.image.length > 0) {
        message.image.forEach(img => {
          message.content.push(
            {
              "type": "image_url",
              "image_url": {
                "url": img,
              }
            }
          );
        });
        delete message.image;
      }

      if (message.audio && message.audio.length > 0) {
        message.audio.forEach(aud => {
          message.content.push(
            {
              "type": "audio_url",
              "audio_url": {
                "url": aud,
              }
            }
          );
        });
        delete message.audio;
      }
    }
  });

  // reset the form and the files
  images = [];
  audios = [];
  document.getElementById("input_image").value = null;
  document.getElementById("input_audio").value = null;
  document.getElementById("input_file").value = null;
  document.getElementById("fileName").innerHTML = "";

  // Choose endpoint based on MCP mode
  const endpoint = mcpMode ? "mcp/v1/chat/completions" : "v1/chat/completions";
  const requestBody = {
    model: model,
    messages: messages,
  };
  
  // Only add stream parameter for regular chat (MCP doesn't support streaming)
  if (!mcpMode) {
    requestBody.stream = true;
  }
  
  let response;
  try {
    // Create AbortController for timeout handling
    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), mcpMode ? 300000 : 30000); // 5 minutes for MCP, 30 seconds for regular
    
    response = await fetch(endpoint, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Accept": "application/json",
      },
      body: JSON.stringify(requestBody),
      signal: controller.signal
    });
    
    clearTimeout(timeoutId);
  } catch (error) {
    if (error.name === 'AbortError') {
      Alpine.store("chat").add(
        "assistant",
        `<span class='error'>Request timeout: MCP processing is taking longer than expected. Please try again.</span>`,
      );
    } else {
      Alpine.store("chat").add(
        "assistant",
        `<span class='error'>Network Error: ${error.message}</span>`,
      );
    }
    toggleLoader(false);
    return;
  }

  if (!response.ok) {
    Alpine.store("chat").add(
      "assistant",
      `<span class='error'>Error: POST ${endpoint} ${response.status}</span>`,
    );
    toggleLoader(false);
    return;
  }

  if (mcpMode) {
    // Handle MCP non-streaming response
    try {
      const data = await response.json();
      // MCP endpoint returns content in choices[0].text, not choices[0].message.content
      const content = data.choices[0]?.text || "";
      
      if (content) {
        // Process thinking tags using shared function
        const { regularContent, thinkingContent } = processThinkingTags(content);
        
        // Add thinking content if present
        if (thinkingContent) {
          Alpine.store("chat").add("thinking", thinkingContent);
        }
        
        // Add regular content if present
        if (regularContent) {
          Alpine.store("chat").add("assistant", regularContent);
        }
      }
      
      // Highlight all code blocks
      hljs.highlightAll();
    } catch (error) {
      Alpine.store("chat").add(
        "assistant",
        `<span class='error'>Error: Failed to parse MCP response</span>`,
      );
    }
  } else {
    // Handle regular streaming response
    const reader = response.body
      ?.pipeThrough(new TextDecoderStream())
      .getReader();

    if (!reader) {
      Alpine.store("chat").add(
        "assistant",
        `<span class='error'>Error: Failed to decode API response</span>`,
      );
      return;
    }

    // Function to add content to the chat and handle DOM updates efficiently
    const addToChat = (token) => {
      const chatStore = Alpine.store("chat");
      chatStore.add("assistant", token);
      // Efficiently scroll into view without triggering multiple reflows
      // const messages = document.getElementById('messages');
      // messages.scrollTop = messages.scrollHeight;
    };

    let buffer = "";
    let contentBuffer = [];
    let thinkingContent = "";
    let isThinking = false;
    let lastThinkingMessageIndex = -1;

    try {
      while (true) {
        const { value, done } = await reader.read();
        if (done) break;

        buffer += value;

        let lines = buffer.split("\n");
        buffer = lines.pop(); // Retain any incomplete line in the buffer

        lines.forEach((line) => {
          if (line.length === 0 || line.startsWith(":")) return;
          if (line === "data: [DONE]") {
            return;
          }

          if (line.startsWith("data: ")) {
            try {
              const jsonData = JSON.parse(line.substring(6));
              const token = jsonData.choices[0].delta.content;

              if (token) {
                // Check for thinking tags
                if (token.includes("<thinking>") || token.includes("<think>")) {
                  isThinking = true;
                  thinkingContent = "";
                  lastThinkingMessageIndex = -1;
                  return;
                }
                if (token.includes("</thinking>") || token.includes("</think>")) {
                  isThinking = false;
                  if (thinkingContent.trim()) {
                    // Only add the final thinking message if we don't already have one
                    if (lastThinkingMessageIndex === -1) {
                      Alpine.store("chat").add("thinking", thinkingContent);
                    }
                  }
                  return;
                }

                // Handle content based on thinking state
                if (isThinking) {
                  thinkingContent += token;
                  // Update the last thinking message or create a new one
                  if (lastThinkingMessageIndex === -1) {
                    // Create new thinking message
                    Alpine.store("chat").add("thinking", thinkingContent);
                    lastThinkingMessageIndex = Alpine.store("chat").history.length - 1;
                  } else {
                    // Update existing thinking message
                    const chatStore = Alpine.store("chat");
                    const lastMessage = chatStore.history[lastThinkingMessageIndex];
                    if (lastMessage && lastMessage.role === "thinking") {
                      lastMessage.content = thinkingContent;
                      lastMessage.html = DOMPurify.sanitize(marked.parse(thinkingContent));
                    }
                  }
                } else {
                  contentBuffer.push(token);
                }
              }
            } catch (error) {
              console.error("Failed to parse line:", line, error);
            }
          }
        });

        // Efficiently update the chat in batch
        if (contentBuffer.length > 0) {
          addToChat(contentBuffer.join(""));
          contentBuffer = [];
        }
      }

      // Final content flush if any data remains
      if (contentBuffer.length > 0) {
        addToChat(contentBuffer.join(""));
      }
      if (thinkingContent.trim() && lastThinkingMessageIndex === -1) {
        Alpine.store("chat").add("thinking", thinkingContent);
      }

      // Highlight all code blocks once at the end
      hljs.highlightAll();
    } catch (error) {
      Alpine.store("chat").add(
        "assistant",
        `<span class='error'>Error: Failed to process stream</span>`,
      );
    } finally {
      // Perform any cleanup if necessary
      reader.releaseLock();
    }
  }

  // Remove class "loader" from the element with "loader" id
  toggleLoader(false);

  // scroll to the bottom of the chat
  document.getElementById('messages').scrollIntoView(false)
  // set focus to the input
  document.getElementById("input").focus();
}

document.getElementById("system_prompt").addEventListener("submit", submitSystemPrompt);
document.getElementById("prompt").addEventListener("submit", submitPrompt);
document.getElementById("input").focus();
document.getElementById("input_image").addEventListener("change", readInputImage);
document.getElementById("input_audio").addEventListener("change", readInputAudio);
document.getElementById("input_file").addEventListener("change", readInputFile);

storesystemPrompt = localStorage.getItem("system_prompt");
if (storesystemPrompt) {
  document.getElementById("systemPrompt").value = storesystemPrompt;
} else {
  document.getElementById("systemPrompt").value = null;
}

marked.setOptions({
  highlight: function (code) {
    return hljs.highlightAuto(code).value;
  },
});

document.addEventListener("alpine:init", () => {
  Alpine.store("chat", {
    history: [],
    languages: [undefined],
    systemPrompt: "",
    mcpMode: false,
    clear() {
      this.history.length = 0;
    },
    add(role, content, image, audio) {
      const N = this.history.length - 1;
      // For thinking messages, always create a new message
      if (role === "thinking") {
        let c = "";
        const lines = content.split("\n");
        lines.forEach((line) => {
          c += DOMPurify.sanitize(marked.parse(line));
        });
        this.history.push({ role, content, html: c, image, audio });
      }
      // For other messages, merge if same role
      else if (this.history.length && this.history[N].role === role) {
        this.history[N].content += content;
        this.history[N].html = DOMPurify.sanitize(
          marked.parse(this.history[N].content)
        );
        // Merge new images and audio with existing ones
        if (image && image.length > 0) {
          this.history[N].image = [...(this.history[N].image || []), ...image];
        }
        if (audio && audio.length > 0) {
          this.history[N].audio = [...(this.history[N].audio || []), ...audio];
        }
      } else {
        let c = "";
        const lines = content.split("\n");
        lines.forEach((line) => {
          c += DOMPurify.sanitize(marked.parse(line));
        });
        this.history.push({ 
          role, 
          content, 
          html: c, 
          image: image || [], 
          audio: audio || [] 
        });
      }
      document.getElementById('messages').scrollIntoView(false);
      const parser = new DOMParser();
      const html = parser.parseFromString(
        this.history[this.history.length - 1].html,
        "text/html"
      );
      const code = html.querySelectorAll("pre code");
      if (!code.length) return;
      code.forEach((el) => {
        const language = el.className.split("language-")[1];
        if (this.languages.includes(language)) return;
        const script = document.createElement("script");
        script.src = `https://cdn.jsdelivr.net/gh/highlightjs/cdn-release@11.8.0/build/languages/${language}.min.js`;
        document.head.appendChild(script);
        this.languages.push(language);
      });
    },
    messages() {
      return this.history.map((message) => ({
        role: message.role,
        content: message.content,
        image: message.image,
        audio: message.audio,
      }));
    },
  });
});
