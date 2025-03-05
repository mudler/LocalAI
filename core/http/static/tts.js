// Initialize Alpine store for API key management
document.addEventListener('alpine:init', () => {
  Alpine.store('chat', {  });
});

function genAudio(event) {
  event.preventDefault();
  const input = document.getElementById("input").value;

  if (!input.trim()) {
    showNotification('error', 'Please enter text to convert to speech');
    return;
  }

  tts(input);
}

function showNotification(type, message) {
  // Remove any existing notification
  const existingNotification = document.getElementById('notification');
  if (existingNotification) {
    existingNotification.remove();
  }
  
  // Create new notification
  const notification = document.createElement('div');
  notification.id = 'notification';
  notification.classList.add(
    'fixed', 'top-24', 'right-4', 'z-50', 'p-4', 'rounded-lg', 'shadow-lg',
    'transform', 'transition-all', 'duration-300', 'ease-in-out', 'translate-y-0',
    'flex', 'items-center', 'gap-2'
  );
  
  // Style based on notification type
  if (type === 'error') {
    notification.classList.add('bg-red-900/90', 'border', 'border-red-700', 'text-red-200');
    notification.innerHTML = '<i class="fas fa-circle-exclamation text-red-400 mr-2"></i>' + message;
  } else if (type === 'warning') {
    notification.classList.add('bg-yellow-900/90', 'border', 'border-yellow-700', 'text-yellow-200');
    notification.innerHTML = '<i class="fas fa-triangle-exclamation text-yellow-400 mr-2"></i>' + message;
  } else if (type === 'success') {
    notification.classList.add('bg-green-900/90', 'border', 'border-green-700', 'text-green-200');
    notification.innerHTML = '<i class="fas fa-circle-check text-green-400 mr-2"></i>' + message;
  } else {
    notification.classList.add('bg-blue-900/90', 'border', 'border-blue-700', 'text-blue-200');
    notification.innerHTML = '<i class="fas fa-circle-info text-blue-400 mr-2"></i>' + message;
  }
  
  // Add close button
  const closeBtn = document.createElement('button');
  closeBtn.innerHTML = '<i class="fas fa-xmark"></i>';
  closeBtn.classList.add('ml-auto', 'text-gray-400', 'hover:text-white', 'transition-colors');
  closeBtn.onclick = () => {
    notification.classList.add('opacity-0', 'translate-y-[-20px]');
    setTimeout(() => notification.remove(), 300);
  };
  notification.appendChild(closeBtn);
  
  // Add to DOM
  document.body.appendChild(notification);
  
  // Animate in
  setTimeout(() => {
    notification.classList.add('opacity-0', 'translate-y-[-20px]');
    notification.offsetHeight; // Force reflow
    notification.classList.remove('opacity-0', 'translate-y-[-20px]');
  }, 10);
  
  // Auto dismiss after 5 seconds
  setTimeout(() => {
    if (document.getElementById('notification')) {
      notification.classList.add('opacity-0', 'translate-y-[-20px]');
      setTimeout(() => notification.remove(), 300);
    }
  }, 5000);
}

async function tts(input) {
  // Show loader and prepare UI
  const loader = document.getElementById("loader");
  const inputField = document.getElementById("input");
  const resultDiv = document.getElementById("result");
  
  loader.style.display = "block";
  inputField.value = "";
  inputField.disabled = true;
  resultDiv.innerHTML = '<div class="text-center text-gray-400 italic">Processing your request...</div>';

  // Get the model and make API request
  const model = document.getElementById("tts-model").value;
  try {
    const response = await fetch("tts", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
      },
      body: JSON.stringify({
        model: model,
        input: input,
      }),
    });
    
    if (!response.ok) {
      const jsonData = await response.json();
      resultDiv.innerHTML = `
        <div class="bg-red-900/30 border border-red-700/50 rounded-lg p-4 text-center">
          <i class="fas fa-circle-exclamation text-red-400 text-2xl mb-2"></i>
          <p class="text-red-300 font-medium">${jsonData.error.message || 'An error occurred'}</p>
        </div>
      `;
      showNotification('error', 'Failed to generate audio');
      return;
    }

    // Handle successful response
    const blob = await response.blob();
    const audioUrl = window.URL.createObjectURL(blob);
    
    // Create audio player
    const audioPlayer = document.createElement('div');
    audioPlayer.className = 'flex flex-col items-center space-y-4 w-full';
    
    // Create audio element with styled controls
    const audio = document.createElement('audio');
    audio.controls = true;
    audio.src = audioUrl;
    audio.className = 'w-full my-4';
    audioPlayer.appendChild(audio);
    
    // Create action buttons container
    const actionButtons = document.createElement('div');
    actionButtons.className = 'flex flex-wrap justify-center gap-3';
    
    // Download button
    const downloadLink = document.createElement('a');
    downloadLink.href = audioUrl;
    downloadLink.download = `tts-${model}-${new Date().toISOString().slice(0, 10)}.mp3`;
    downloadLink.className = 'group flex items-center bg-blue-600 hover:bg-blue-700 text-white py-2 px-4 rounded-lg transition duration-300 ease-in-out transform hover:scale-105 hover:shadow-lg';
    downloadLink.innerHTML = `
      <i class="fas fa-download mr-2"></i>
      <span>Download</span>
      <i class="fas fa-arrow-right opacity-0 group-hover:opacity-100 group-hover:translate-x-2 ml-2 transition-all duration-300"></i>
    `;
    actionButtons.appendChild(downloadLink);
    
    // Replay button
    const replayButton = document.createElement('button');
    replayButton.className = 'group flex items-center bg-purple-600 hover:bg-purple-700 text-white py-2 px-4 rounded-lg transition duration-300 ease-in-out transform hover:scale-105 hover:shadow-lg';
    replayButton.innerHTML = `
      <i class="fas fa-rotate-right mr-2"></i>
      <span>Replay</span>
    `;
    replayButton.onclick = () => audio.play();
    actionButtons.appendChild(replayButton);
    
    // Add text display
    const textDisplay = document.createElement('div');
    textDisplay.className = 'mt-4 p-4 bg-gray-800/50 border border-gray-700/50 rounded-lg text-gray-300 text-center italic';
    textDisplay.textContent = `"${input}"`;
    
    // Add all elements to result div
    audioPlayer.appendChild(actionButtons);
    resultDiv.innerHTML = '';
    resultDiv.appendChild(audioPlayer);
    resultDiv.appendChild(textDisplay);
    
    // Play audio automatically
    audio.play();
    
    // Show success notification
    showNotification('success', 'Audio generated successfully');
    
  } catch (error) {
    console.error('Error generating audio:', error);
    resultDiv.innerHTML = `
      <div class="bg-red-900/30 border border-red-700/50 rounded-lg p-4 text-center">
        <i class="fas fa-circle-exclamation text-red-400 text-2xl mb-2"></i>
        <p class="text-red-300 font-medium">Network error: Failed to connect to the server</p>
      </div>
    `;
    showNotification('error', 'Network error occurred');
  } finally {
    // Reset UI state
    loader.style.display = "none";
    inputField.disabled = false;
    inputField.focus();
  }
}

// Set up event listeners when DOM is loaded
document.addEventListener('DOMContentLoaded', () => {
  document.getElementById("input").focus();
  document.getElementById("tts").addEventListener("submit", genAudio);
  document.getElementById("loader").style.display = "none";
 
  // Add basic keyboard shortcuts
  document.addEventListener('keydown', (e) => {
    // Submit on Ctrl+Enter
    if (e.key === 'Enter' && e.ctrlKey && document.activeElement.id === 'input') {
      e.preventDefault();
      document.getElementById("tts").dispatchEvent(new Event('submit'));
    }
  });
});