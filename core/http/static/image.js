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
  

function genImage(event) {
  event.preventDefault();
  const input = document.getElementById("input").value;
  const key = localStorage.getItem("key");

  promptDallE(key, input);

}
  
async function promptDallE(key, input) {
  document.getElementById("loader").style.display = "block";
  document.getElementById("input").value = "";
  document.getElementById("input").disabled = true;

  const model = document.getElementById("image-model").value;
  const response = await fetch("/v1/images/generations", {
    method: "POST",
    headers: {
      Authorization: `Bearer ${key}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      model: model,
      steps: 10,
      prompt: input,
      n: 1,
      size: "512x512",
    }),
  });
  const json = await response.json();
  if (json.error) {
    // Display error if there is one
    var div = document.getElementById('result');  // Get the div by its ID
    div.innerHTML = '<p style="color:red;">' + json.error.message + '</p>';
    return;
  }
  const url = json.data[0].url;

  var div = document.getElementById('result');  // Get the div by its ID
  var img = document.createElement('img');         // Create a new img element
  img.src = url;  // Set the source of the image
  img.alt = 'Generated image';            // Set the alt text of the image

  div.innerHTML = '';                             // Clear the existing content of the div
  div.appendChild(img);                           // Add the new img element to the div

  document.getElementById("loader").style.display = "none";
  document.getElementById("input").disabled = false;
  document.getElementById("input").focus();
}

document.getElementById("key").addEventListener("submit", submitKey);
document.getElementById("input").focus();
document.getElementById("genimage").addEventListener("submit", genImage);
document.getElementById("loader").style.display = "none";

const storeKey = localStorage.getItem("key");
if (storeKey) {
  document.getElementById("apiKey").value = storeKey;
}

