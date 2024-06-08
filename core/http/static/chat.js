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

function submitKey(event) {
    event.preventDefault();
    localStorage.setItem("key", document.getElementById("apiKey").value);
    document.getElementById("apiKey").blur();
}

function submitSystemPrompt(event) {
  event.preventDefault();
  localStorage.setItem("system_prompt", document.getElementById("systemPrompt").value);
  document.getElementById("systemPrompt").blur();
}

var image = "";

function submitPrompt(event) {
  event.preventDefault();

  const input = document.getElementById("input").value;
  Alpine.store("chat").add("user", input, image);
  document.getElementById("input").value = "";
  const key = localStorage.getItem("key");
  const systemPrompt = localStorage.getItem("system_prompt");

  promptGPT(systemPrompt, key, input);
}

function readInputImage() {

  if (!this.files || !this.files[0]) return;

  const FR = new FileReader();

  FR.addEventListener("load", function(evt) {
    image = evt.target.result;
  });

  FR.readAsDataURL(this.files[0]);
}


  async function promptGPT(systemPrompt, key, input) {
    const model = document.getElementById("chat-model").value;
    // Set class "loader" to the element with "loader" id
    //document.getElementById("loader").classList.add("loader");
    // Make the "loader" visible
    document.getElementById("loader").style.display = "block";
    document.getElementById("input").disabled = true;
    document.getElementById('messages').scrollIntoView(false)

    messages = Alpine.store("chat").messages();

    // if systemPrompt isn't empty, push it at the start of messages
    if (systemPrompt) {
      messages.unshift({
        role: "system",
        content: systemPrompt
      });
    }

    // loop all messages, and check if there are images. If there are, we need to change the content field
    messages.forEach((message) => {
      if (message.image) {
        // The content field now becomes an array
        message.content = [
          {
            "type": "text",
            "text": message.content
          }
        ]
        message.content.push(
          {
            "type": "image_url",
            "image_url": {
              "url": message.image,
            }
          }
        );

        // remove the image field
        delete message.image;
      }
    });

       // reset the form and the image
       image = "";
       document.getElementById("input_image").value = null;
       document.getElementById("fileName").innerHTML = "";

    // if (image) {
    //   // take the last element content's and add the image
    //   last_message = messages[messages.length - 1]
    //   // The content field now becomes an array
    //   last_message.content = [
    //     {
    //       "type": "text",
    //       "text": last_message.content
    //     }
    //    ]
    //   last_message.content.push(
    //     {
    //       "type": "image_url",
    //       "image_url": {
    //         "url": image,
    //       }
    //     }
    //   );
    //   // and we replace it in the messages array
    //   messages[messages.length - 1] = last_message

    //   // reset the form and the image
    //   image = "";
    //   document.getElementById("input_image").value = null;
    //   document.getElementById("fileName").innerHTML = "";
    // }

    // Source: https://stackoverflow.com/a/75751803/11386095
    const response = await fetch("/v1/chat/completions", {
      method: "POST",
      headers: {
        Authorization: `Bearer ${key}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        model: model,
        messages: messages,
        stream: true,
      }),
    });

    if (!response.ok) {
      Alpine.store("chat").add(
        "assistant",
        `<span class='error'>Error: POST /v1/chat/completions ${response.status}</span>`,
      );
      return;
    }

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
      const messages = document.getElementById('messages');
      messages.scrollTop = messages.scrollHeight;
    };

    let buffer = "";
    let contentBuffer = [];

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
                contentBuffer.push(token);
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

      // Highlight all code blocks once at the end
      hljs.highlightAll();
    } catch (error) {
      console.error("An error occurred while reading the stream:", error);
      Alpine.store("chat").add(
        "assistant",
        `<span class='error'>Error: Failed to process stream</span>`,
      );
    } finally {
      // Perform any cleanup if necessary
      reader.releaseLock();
    }

    // Remove class "loader" from the element with "loader" id
    //document.getElementById("loader").classList.remove("loader");
    document.getElementById("loader").style.display = "none";
    // enable input
    document.getElementById("input").disabled = false;
    // scroll to the bottom of the chat
    document.getElementById('messages').scrollIntoView(false)
    // set focus to the input
    document.getElementById("input").focus();
  }

  document.getElementById("key").addEventListener("submit", submitKey);
  document.getElementById("system_prompt").addEventListener("submit", submitSystemPrompt);

  document.getElementById("prompt").addEventListener("submit", submitPrompt);
  document.getElementById("input").focus();
  document.getElementById("input_image").addEventListener("change", readInputImage);

  storeKey = localStorage.getItem("key");
  if (storeKey) {
    document.getElementById("apiKey").value = storeKey;
  } else {
    document.getElementById("apiKey").value = null;
  }

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
