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

function genVideo(event) {
  event.preventDefault();
  promptVideo();
}

async function promptVideo() {
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
  const model = document.getElementById("video-model").value;
  const size = document.getElementById("video-size").value;
  const negativePrompt = document.getElementById("negative-prompt").value.trim();
  
  // Parse size into width and height
  const sizeParts = size.split("x");
  let width = 512;
  let height = 512;
  if (sizeParts.length === 2) {
    width = parseInt(sizeParts[0]) || 512;
    height = parseInt(sizeParts[1]) || 512;
  }

  // Video-specific parameters
  const secondsInput = document.getElementById("video-seconds").value.trim();
  const seconds = secondsInput ? secondsInput : undefined;
  const fpsInput = document.getElementById("video-fps").value.trim();
  const fps = fpsInput ? parseInt(fpsInput) : 16;
  const framesInput = document.getElementById("video-frames").value.trim();
  const numFrames = framesInput ? parseInt(framesInput) : undefined;

  // Advanced parameters
  const stepInput = document.getElementById("video-steps").value.trim();
  const step = stepInput ? parseInt(stepInput) : undefined;
  const seedInput = document.getElementById("video-seed").value.trim();
  const seed = seedInput ? parseInt(seedInput) : undefined;
  const cfgScaleInput = document.getElementById("video-cfg-scale").value.trim();
  const cfgScale = cfgScaleInput ? parseFloat(cfgScaleInput) : undefined;

  // Prepare request body
  const requestBody = {
    model: model,
    prompt: prompt,
    width: width,
    height: height,
    fps: fps,
  };

  if (negativePrompt) {
    requestBody.negative_prompt = negativePrompt;
  }

  if (seconds !== undefined) {
    requestBody.seconds = seconds;
  }

  if (numFrames !== undefined) {
    requestBody.num_frames = numFrames;
  }

  if (step !== undefined) {
    requestBody.step = step;
  }

  if (seed !== undefined) {
    requestBody.seed = seed;
  }

  if (cfgScale !== undefined) {
    requestBody.cfg_scale = cfgScale;
  }

  // Handle file inputs
  try {
    // Start image (for img2video)
    const startImageInput = document.getElementById("start-image");
    if (startImageInput.files.length > 0) {
      const base64 = await fileToBase64(startImageInput.files[0]);
      requestBody.start_image = base64;
    }

    // End image
    const endImageInput = document.getElementById("end-image");
    if (endImageInput.files.length > 0) {
      const base64 = await fileToBase64(endImageInput.files[0]);
      requestBody.end_image = base64;
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

  // Make API request to LocalAI endpoint
  try {
    const response = await fetch("video", {
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

    // Display generated video
    if (json.data && json.data.length > 0) {
      json.data.forEach((item, index) => {
        const videoContainer = document.createElement("div");
        videoContainer.className = "flex flex-col";

        // Create video element
        const video = document.createElement("video");
        video.controls = true;
        video.className = "w-full h-auto rounded-lg";
        video.preload = "metadata";
        
        if (item.url) {
          video.src = item.url;
        } else if (item.b64_json) {
          video.src = "data:video/mp4;base64," + item.b64_json;
        } else {
          return; // Skip invalid items
        }
        
        videoContainer.appendChild(video);

        // Create caption container
        const captionDiv = document.createElement("div");
        captionDiv.className = "mt-2 p-2 bg-[var(--color-bg-secondary)] rounded-lg text-xs";

        // Prompt caption
        const promptCaption = document.createElement("p");
        promptCaption.className = "text-[var(--color-text-primary)] mb-1.5 break-words";
        promptCaption.innerHTML = '<strong>Prompt:</strong> ' + escapeHtml(prompt);
        captionDiv.appendChild(promptCaption);

        // Negative prompt if provided
        if (negativePrompt) {
          const negativeCaption = document.createElement("p");
          negativeCaption.className = "text-[var(--color-text-secondary)] mb-1.5 break-words";
          negativeCaption.innerHTML = '<strong>Negative Prompt:</strong> ' + escapeHtml(negativePrompt);
          captionDiv.appendChild(negativeCaption);
        }

        // Generation details
        const detailsDiv = document.createElement("div");
        detailsDiv.className = "flex flex-wrap gap-3 text-[10px] text-[var(--color-text-secondary)] mt-1.5";
        detailsDiv.innerHTML = `
          <span><strong>Size:</strong> ${width}x${height}</span>
          ${fps ? `<span><strong>FPS:</strong> ${fps}</span>` : ''}
          ${numFrames !== undefined ? `<span><strong>Frames:</strong> ${numFrames}</span>` : ''}
          ${seconds !== undefined ? `<span><strong>Duration:</strong> ${seconds}s</span>` : ''}
          ${step !== undefined ? `<span><strong>Steps:</strong> ${step}</span>` : ''}
          ${seed !== undefined ? `<span><strong>Seed:</strong> ${seed}</span>` : ''}
          ${cfgScale !== undefined ? `<span><strong>CFG Scale:</strong> ${cfgScale}</span>` : ''}
        `;
        captionDiv.appendChild(detailsDiv);

        // Button container
        const buttonContainer = document.createElement("div");
        buttonContainer.className = "mt-1.5 flex gap-2";

        // Copy prompt button
        const copyBtn = document.createElement("button");
        copyBtn.className = "px-2 py-0.5 text-[10px] bg-[var(--color-primary)] text-white rounded hover:opacity-80";
        copyBtn.innerHTML = '<i class="fas fa-copy mr-1"></i>Copy Prompt';
        copyBtn.onclick = () => {
          navigator.clipboard.writeText(prompt).then(() => {
            copyBtn.innerHTML = '<i class="fas fa-check mr-1"></i>Copied!';
            setTimeout(() => {
              copyBtn.innerHTML = '<i class="fas fa-copy mr-1"></i>Copy Prompt';
            }, 2000);
          });
        };
        buttonContainer.appendChild(copyBtn);

        // Download video button
        const downloadBtn = document.createElement("button");
        downloadBtn.className = "px-2 py-0.5 text-[10px] bg-[var(--color-primary)] text-white rounded hover:opacity-80";
        downloadBtn.innerHTML = '<i class="fas fa-download mr-1"></i>Download Video';
        downloadBtn.onclick = () => {
          downloadVideo(item, downloadBtn);
        };
        buttonContainer.appendChild(downloadBtn);

        captionDiv.appendChild(buttonContainer);

        videoContainer.appendChild(captionDiv);
        resultDiv.appendChild(videoContainer);
      });
      // Hide placeholder when videos are displayed
      if (resultPlaceholder) {
        resultPlaceholder.style.display = "none";
      }
    } else {
      resultDiv.innerHTML = '<p class="text-xs text-[var(--color-text-secondary)] p-2">No videos were generated.</p>';
      if (resultPlaceholder) {
        resultPlaceholder.style.display = "none";
      }
    }

  } catch (error) {
    console.error("Error generating video:", error);
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

// Helper function to download video
function downloadVideo(item, button) {
  try {
    let videoUrl;
    let filename = "generated-video.mp4";

    if (item.url) {
      // If we have a URL, use it directly
      videoUrl = item.url;
      // Extract filename from URL if possible
      const urlParts = item.url.split("/");
      if (urlParts.length > 0) {
        const lastPart = urlParts[urlParts.length - 1];
        if (lastPart && lastPart.includes(".")) {
          filename = lastPart;
        }
      }
    } else if (item.b64_json) {
      // Convert base64 to blob
      const byteCharacters = atob(item.b64_json);
      const byteNumbers = new Array(byteCharacters.length);
      for (let i = 0; i < byteCharacters.length; i++) {
        byteNumbers[i] = byteCharacters.charCodeAt(i);
      }
      const byteArray = new Uint8Array(byteNumbers);
      const blob = new Blob([byteArray], { type: "video/mp4" });
      videoUrl = URL.createObjectURL(blob);
    } else {
      console.error("No video data available for download");
      return;
    }

    // Create a temporary anchor element to trigger download
    const link = document.createElement("a");
    link.href = videoUrl;
    link.download = filename;
    link.style.display = "none";
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);

    // Clean up object URL if we created one
    if (item.b64_json && videoUrl.startsWith("blob:")) {
      setTimeout(() => URL.revokeObjectURL(videoUrl), 100);
    }

    // Show feedback
    const originalHTML = button.innerHTML;
    button.innerHTML = '<i class="fas fa-check mr-1"></i>Downloaded!';
    setTimeout(() => {
      button.innerHTML = originalHTML;
    }, 2000);
  } catch (error) {
    console.error("Error downloading video:", error);
    button.innerHTML = '<i class="fas fa-exclamation-triangle mr-1"></i>Error';
    setTimeout(() => {
      button.innerHTML = '<i class="fas fa-download mr-1"></i>Download Video';
    }, 2000);
  }
}

// Initialize
document.addEventListener("DOMContentLoaded", function() {
  const input = document.getElementById("input");
  const form = document.getElementById("genvideo");

  if (input) {
    input.focus();
  }

  if (form) {
    form.addEventListener("submit", genVideo);
  }

  // Handle Enter key press in the prompt input (but allow Shift+Enter for new lines)
  if (input) {
    input.addEventListener("keydown", function(event) {
      if (event.key === "Enter" && !event.shiftKey) {
        event.preventDefault();
        genVideo(event);
      }
    });
  }

  // Hide loader initially
  const loader = document.getElementById("loader");
  if (loader) {
    loader.classList.add("hidden");
  }
});
