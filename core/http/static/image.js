function genImage(event) {
  event.preventDefault();
  const input = document.getElementById("input").value;

  promptDallE(input);
}
  
async function promptDallE(input) {
  document.getElementById("loader").style.display = "block";
  document.getElementById("input").value = "";
  document.getElementById("input").disabled = true;

  const model = document.getElementById("image-model").value;
  const size = document.getElementById("image-size").value;
  const response = await fetch("v1/images/generations", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      model: model,
      steps: 10,
      prompt: input,
      n: 1,
      size: size,
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

document.getElementById("input").focus();
document.getElementById("genimage").addEventListener("submit", genImage);

// Handle Enter key press in the prompt input
document.getElementById("input").addEventListener("keypress", function(event) {
    if (event.key === "Enter") {
        event.preventDefault();
        genImage(event);
    }
});

document.getElementById("loader").style.display = "none";
