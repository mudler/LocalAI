function submitKey(event) {
    event.preventDefault();
    localStorage.setItem("key", document.getElementById("apiKey").value);
    document.getElementById("apiKey").blur();
  }
  

function genAudio(event) {
  event.preventDefault();
  const input = document.getElementById("input").value;
  const key = localStorage.getItem("key");

  tts(key, input);
}
  
async function tts(key, input) {
  document.getElementById("loader").style.display = "block";
  document.getElementById("input").value = "";
  document.getElementById("input").disabled = true;

  const model = document.getElementById("tts-model").value;
  const response = await fetch("/tts", {
    method: "POST",
    headers: {
      Authorization: `Bearer ${key}`,
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      model: model,
      input: input,
    }),
  });
  if (!response.ok) {
    const jsonData = await response.json(); // Now safely parse JSON
    var div = document.getElementById('result');
    div.innerHTML = '<p style="color:red;">Error: ' +jsonData.error.message + '</p>';
    return;
  }

  var div = document.getElementById('result');  // Get the div by its ID
  var link=document.createElement('a');
  link.className = "m-2 float-right inline-block rounded bg-primary px-6 pb-2.5 mb-3 pt-2.5 text-xs font-medium uppercase leading-normal text-white shadow-primary-3 transition duration-150 ease-in-out hover:bg-primary-accent-300 hover:shadow-primary-2 focus:bg-primary-accent-300 focus:shadow-primary-2 focus:outline-none focus:ring-0 active:bg-primary-600 active:shadow-primary-2 dark:shadow-black/30 dark:hover:shadow-dark-strong dark:focus:shadow-dark-strong dark:active:shadow-dark-strong";
  link.innerHTML = "<i class='fa-solid fa-download'></i> Download result";
  const blob = await response.blob();
  link.href=window.URL.createObjectURL(blob);

  div.innerHTML = '';                             // Clear the existing content of the div
  div.appendChild(link);                           // Add the new img element to the div
  console.log(link)
  document.getElementById("loader").style.display = "none";
  document.getElementById("input").disabled = false;
  document.getElementById("input").focus();
}

document.getElementById("key").addEventListener("submit", submitKey);
document.getElementById("input").focus();
document.getElementById("tts").addEventListener("submit", genAudio);
document.getElementById("loader").style.display = "none";

const storeKey = localStorage.getItem("key");
if (storeKey) {
  document.getElementById("apiKey").value = storeKey;
}

