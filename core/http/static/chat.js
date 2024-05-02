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
  
function submitPrompt(event) {
  event.preventDefault();

  const input = document.getElementById("input").value;
  Alpine.store("chat").add("user", input);
  document.getElementById("input").value = "";
  const key = localStorage.getItem("key");

  promptGPT(key, input);
}


  async function promptGPT(key, input) {
    const model = document.getElementById("chat-model").value;
    // Set class "loader" to the element with "loader" id
    //document.getElementById("loader").classList.add("loader");
    // Make the "loader" visible
    document.getElementById("loader").style.display = "block";
    document.getElementById("input").disabled = true;
    document.getElementById('messages').scrollIntoView(false)

    // Source: https://stackoverflow.com/a/75751803/11386095
    const response = await fetch("/v1/chat/completions", {
      method: "POST",
      headers: {
        Authorization: `Bearer ${key}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        model: model,
        messages: Alpine.store("chat").messages(),
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
  
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      let dataDone = false;
      const arr = value.split("\n");
      arr.forEach((data) => {
        if (data.length === 0) return;
        if (data.startsWith(":")) return;
        if (data === "data: [DONE]") {
          dataDone = true;
          return;
        }
        const token = JSON.parse(data.substring(6)).choices[0].delta.content;
        if (!token) {
          return;
        }
        hljs.highlightAll();
        Alpine.store("chat").add("assistant", token);
        document.getElementById('messages').scrollIntoView(false)
      });
      hljs.highlightAll();
      if (dataDone) break;
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
  document.getElementById("prompt").addEventListener("submit", submitPrompt);
  document.getElementById("input").focus();

  const storeKey = localStorage.getItem("key");
  if (storeKey) {
    document.getElementById("apiKey").value = storeKey;
  }
  
  marked.setOptions({
    highlight: function (code) {
      return hljs.highlightAuto(code).value;
    },
  });
