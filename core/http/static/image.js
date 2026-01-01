// Helper function to convert file to base64
function fileToBase64(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => {
      // Remove data:image/...;base64, prefix if present
      const base64 = reader.result.split(',')[1] || reader.result;
      resolve(base64);
    };
    reader.onerror = reject;
    reader.readAsDataURL(file);
  });
}

// Helper function to read multiple files
async function filesToBase64Array(fileList) {
  const base64Array = [];
  for (let i = 0; i < fileList.length; i++) {
    const base64 = await fileToBase64(fileList[i]);
    base64Array.push(base64);
  }
  return base64Array;
}

function genImage(event) {
  event.preventDefault();
  promptDallE();
}

async function promptDallE() {
  const loader = document.getElementById("loader");
  const input = document.getElementById("input");
  const generateBtn = document.getElementById("generate-btn");
  const resultDiv = document.getElementById("result");
  const resultPlaceholder = document.getElementById("result-placeholder");

  // Show loader and disable form
  loader.classList.remove("hidden");
  if (resultPlaceholder) {
    resultPlaceholder.style.display = "none";
  }
  input.disabled = true;
  generateBtn.disabled = true;

  // Store the prompt for later restoration
  const prompt = input.value.trim();
  if (!prompt) {
    alert("Please enter a prompt");
    loader.classList.add("hidden");
    if (resultPlaceholder) {
      resultPlaceholder.style.display = "flex";
    }
    input.disabled = false;
    generateBtn.disabled = false;
    return;
  }

  // Collect all form values
  const model = document.getElementById("image-model").value;
  const size = document.getElementById("image-size").value;
  const negativePrompt = document.getElementById("negative-prompt").value.trim();
  const n = parseInt(document.getElementById("image-count").value) || 1;
  const stepInput = document.getElementById("image-steps").value.trim();
  const step = stepInput ? parseInt(stepInput) : undefined;
  const seedInput = document.getElementById("image-seed").value.trim();
  const seed = seedInput ? parseInt(seedInput) : undefined;

  // Prepare request body
  // Combine prompt and negative prompt with "|" separator (backend expects this format)
  let combinedPrompt = prompt;
  if (negativePrompt) {
    combinedPrompt = prompt + "|" + negativePrompt;
  }

  const requestBody = {
    model: model,
    prompt: combinedPrompt,
    n: n,
    size: size,
  };

  if (step !== undefined) {
    requestBody.step = step;
  }

  if (seed !== undefined) {
    requestBody.seed = seed;
  }

  // Handle file inputs
  try {
    // Source image (single file for img2img)
    const sourceImageInput = document.getElementById("source-image");
    if (sourceImageInput.files.length > 0) {
      const base64 = await fileToBase64(sourceImageInput.files[0]);
      requestBody.file = base64;
    }

    // Reference images (collect from all dynamic inputs)
    const refImageInputs = document.querySelectorAll('.reference-image-file');
    const refImageFiles = [];
    for (const input of refImageInputs) {
      if (input.files.length > 0) {
        refImageFiles.push(input.files[0]);
      }
    }
    if (refImageFiles.length > 0) {
      const base64Array = await filesToBase64Array(refImageFiles);
      requestBody.ref_images = base64Array;
    }
  } catch (error) {
    console.error("Error processing image files:", error);
    resultDiv.innerHTML = '<p class="text-xs text-red-500 p-2">Error processing image files: ' + error.message + '</p>';
    loader.classList.add("hidden");
    if (resultPlaceholder) {
      resultPlaceholder.style.display = "none";
    }
    input.disabled = false;
    generateBtn.disabled = false;
    return;
  }

  // Make API request
  try {
    const response = await fetch("v1/images/generations", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
      },
      body: JSON.stringify(requestBody),
    });

    const json = await response.json();

    if (json.error) {
      // Display error
      resultDiv.innerHTML = '<p class="text-xs text-red-500 p-2">Error: ' + json.error.message + '</p>';
      loader.classList.add("hidden");
      if (resultPlaceholder) {
        resultPlaceholder.style.display = "none";
      }
      input.disabled = false;
      generateBtn.disabled = false;
      return;
    }

    // Clear result div and hide placeholder
    resultDiv.innerHTML = '';
    if (resultPlaceholder) {
      resultPlaceholder.style.display = "none";
    }

    // Display all generated images
    if (json.data && json.data.length > 0) {
      json.data.forEach((item, index) => {
        const imageContainer = document.createElement("div");
        imageContainer.className = "mb-4 bg-[var(--color-bg-primary)]/50 border border-[#1E293B] rounded-lg p-2";

        // Create image element
        const img = document.createElement("img");
        if (item.url) {
          img.src = item.url;
        } else if (item.b64_json) {
          img.src = "data:image/png;base64," + item.b64_json;
        } else {
          return; // Skip invalid items
        }
        img.alt = prompt;
        img.className = "w-full h-auto rounded-lg mb-2";
        imageContainer.appendChild(img);

        // Create caption container
        const captionDiv = document.createElement("div");
        captionDiv.className = "mt-2 p-2 bg-[var(--color-bg-secondary)] rounded-lg";

        // Prompt caption
        const promptCaption = document.createElement("p");
        promptCaption.className = "text-xs text-[var(--color-text-primary)] mb-1.5";
        promptCaption.innerHTML = '<strong>Prompt:</strong> ' + escapeHtml(prompt);
        captionDiv.appendChild(promptCaption);

        // Negative prompt if provided
        if (negativePrompt) {
          const negativeCaption = document.createElement("p");
          negativeCaption.className = "text-xs text-[var(--color-text-secondary)] mb-1.5";
          negativeCaption.innerHTML = '<strong>Negative Prompt:</strong> ' + escapeHtml(negativePrompt);
          captionDiv.appendChild(negativeCaption);
        }

        // Generation details
        const detailsDiv = document.createElement("div");
        detailsDiv.className = "flex flex-wrap gap-3 text-[10px] text-[var(--color-text-secondary)] mt-1.5";
        detailsDiv.innerHTML = `
          <span><strong>Size:</strong> ${size}</span>
          ${step !== undefined ? `<span><strong>Steps:</strong> ${step}</span>` : ''}
          ${seed !== undefined ? `<span><strong>Seed:</strong> ${seed}</span>` : ''}
        `;
        captionDiv.appendChild(detailsDiv);

        // Copy prompt button
        const copyBtn = document.createElement("button");
        copyBtn.className = "mt-1.5 px-2 py-0.5 text-[10px] bg-[var(--color-primary)] text-white rounded hover:opacity-80";
        copyBtn.innerHTML = '<i class="fas fa-copy mr-1"></i>Copy Prompt';
        copyBtn.onclick = () => {
          navigator.clipboard.writeText(prompt).then(() => {
            copyBtn.innerHTML = '<i class="fas fa-check mr-1"></i>Copied!';
            setTimeout(() => {
              copyBtn.innerHTML = '<i class="fas fa-copy mr-1"></i>Copy Prompt';
            }, 2000);
          });
        };
        captionDiv.appendChild(copyBtn);

        imageContainer.appendChild(captionDiv);
        resultDiv.appendChild(imageContainer);
      });
      // Hide placeholder when images are displayed
      if (resultPlaceholder) {
        resultPlaceholder.style.display = "none";
      }
    } else {
      resultDiv.innerHTML = '<p class="text-xs text-[var(--color-text-secondary)] p-2">No images were generated.</p>';
      if (resultPlaceholder) {
        resultPlaceholder.style.display = "none";
      }
    }

    // Preserve prompt in input field (don't clear it)
    // The prompt is already in the input field, so we don't need to restore it

  } catch (error) {
    console.error("Error generating image:", error);
    resultDiv.innerHTML = '<p class="text-xs text-red-500 p-2">Error: ' + error.message + '</p>';
    if (resultPlaceholder) {
      resultPlaceholder.style.display = "none";
    }
  } finally {
    // Hide loader and re-enable form
    loader.classList.add("hidden");
    input.disabled = false;
    generateBtn.disabled = false;
    input.focus();
  }
}

// Helper function to escape HTML
function escapeHtml(text) {
  const div = document.createElement("div");
  div.textContent = text;
  return div.innerHTML;
}

// Initialize
document.addEventListener("DOMContentLoaded", function() {
  const input = document.getElementById("input");
  const form = document.getElementById("genimage");

  if (input) {
    input.focus();
  }

  if (form) {
    form.addEventListener("submit", genImage);
  }

  // Handle Enter key press in the prompt input (but allow Shift+Enter for new lines)
  if (input) {
    input.addEventListener("keydown", function(event) {
      if (event.key === "Enter" && !event.shiftKey) {
        event.preventDefault();
        genImage(event);
      }
    });
  }

  // Hide loader initially
  const loader = document.getElementById("loader");
  if (loader) {
    loader.classList.add("hidden");
  }
});
