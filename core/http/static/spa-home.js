/**
 * SPA Home View JavaScript
 * Contains Alpine.js components and functions for the home view
 */

// Home input form component
function homeInputForm() {
  return {
    selectedModel: '',
    inputValue: '',
    shiftPressed: false,
    fileName: '',
    imageFiles: [],
    audioFiles: [],
    textFiles: [],
    attachedFiles: [],
    mcpMode: false,
    mcpAvailable: false,
    mcpModels: {},
    currentPlaceholder: 'Send a message...',
    placeholderIndex: 0,
    charIndex: 0,
    isTyping: false,
    typingTimeout: null,
    displayTimeout: null,
    placeholderMessages: [
      'What is Nuclear fusion?',
      'How does a combustion engine work?',
      'Explain quantum computing',
      'What causes climate change?',
      'How do neural networks learn?',
      'What is the theory of relativity?',
      'How does photosynthesis work?',
      'Explain the water cycle',
      'What is machine learning?',
      'How do black holes form?',
      'What is DNA and how does it work?',
      'Explain the greenhouse effect',
      'How does the immune system work?',
      'What is artificial intelligence?',
      'How do solar panels generate electricity?',
      'Explain the process of evolution',
      'What is the difference between weather and climate?',
      'How does the human brain process information?',
      'What is the structure of an atom?',
      'How do vaccines work?',
      'Explain the concept of entropy',
      'What is the speed of light?',
      'How does gravity work?',
      'What is the difference between mass and weight?'
    ],

    init() {
      window.currentPlaceholderText = this.currentPlaceholder;
      this.startTypingAnimation();
      // Build MCP models map from data attributes
      this.buildMCPModelsMap();
      // Select first model by default
      this.$nextTick(() => {
        const select = this.$el.querySelector('select');
        if (select && select.options.length > 1) {
          const firstModelOption = select.options[1];
          if (firstModelOption && firstModelOption.value) {
            this.selectedModel = firstModelOption.value;
            this.checkMCPAvailability();
          }
        }
      });
      // Watch for changes to selectedModel to update MCP availability
      this.$watch('selectedModel', () => {
        this.checkMCPAvailability();
      });
    },

    buildMCPModelsMap() {
      const select = this.$el.querySelector('select');
      if (!select) return;
      this.mcpModels = {};
      for (let i = 0; i < select.options.length; i++) {
        const option = select.options[i];
        if (option.value) {
          const hasMcpAttr = option.getAttribute('data-has-mcp');
          this.mcpModels[option.value] = hasMcpAttr === 'true';
        }
      }
    },

    checkMCPAvailability() {
      if (!this.selectedModel) {
        this.mcpAvailable = false;
        this.mcpMode = false;
        return;
      }
      const hasMCP = this.mcpModels[this.selectedModel] === true;
      this.mcpAvailable = hasMCP;
      if (!hasMCP) {
        this.mcpMode = false;
      }
    },

    startTypingAnimation() {
      if (this.isTyping) return;
      this.typeNextPlaceholder();
    },

    typeNextPlaceholder() {
      if (this.isTyping) return;
      this.isTyping = true;
      this.charIndex = 0;
      const message = this.placeholderMessages[this.placeholderIndex];
      this.currentPlaceholder = '';
      window.currentPlaceholderText = '';

      const typeChar = () => {
        if (this.charIndex < message.length) {
          this.currentPlaceholder = message.substring(0, this.charIndex + 1);
          window.currentPlaceholderText = this.currentPlaceholder;
          this.charIndex++;
          this.typingTimeout = setTimeout(typeChar, 30);
        } else {
          this.isTyping = false;
          window.currentPlaceholderText = this.currentPlaceholder;
          this.displayTimeout = setTimeout(() => {
            this.placeholderIndex = (this.placeholderIndex + 1) % this.placeholderMessages.length;
            this.typeNextPlaceholder();
          }, 2000);
        }
      };

      typeChar();
    },

    pauseTyping() {
      if (this.typingTimeout) {
        clearTimeout(this.typingTimeout);
        this.typingTimeout = null;
      }
      if (this.displayTimeout) {
        clearTimeout(this.displayTimeout);
        this.displayTimeout = null;
      }
      this.isTyping = false;
    },

    resumeTyping() {
      if (!this.inputValue.trim() && !this.isTyping) {
        this.startTypingAnimation();
      }
    },

    handleFocus() {
      if (this.isTyping && this.placeholderIndex < this.placeholderMessages.length) {
        const fullMessage = this.placeholderMessages[this.placeholderIndex];
        this.currentPlaceholder = fullMessage;
        window.currentPlaceholderText = fullMessage;
      }
      this.pauseTyping();
    },

    handleBlur() {
      if (!this.inputValue.trim()) {
        this.resumeTyping();
      }
    },

    handleInput() {
      if (this.inputValue.trim()) {
        this.pauseTyping();
      } else {
        this.resumeTyping();
      }
    },

    handleFileSelection(files, fileType) {
      Array.from(files).forEach(file => {
        const exists = this.attachedFiles.some(f => f.name === file.name && f.type === fileType);
        if (!exists) {
          this.attachedFiles.push({ name: file.name, type: fileType });
        }
      });
    },

    removeAttachedFile(fileType, fileName) {
      const index = this.attachedFiles.findIndex(f => f.name === fileName && f.type === fileType);
      if (index !== -1) {
        this.attachedFiles.splice(index, 1);
      }
      if (fileType === 'image') {
        this.imageFiles = this.imageFiles.filter(f => f.name !== fileName);
      } else if (fileType === 'audio') {
        this.audioFiles = this.audioFiles.filter(f => f.name !== fileName);
      } else if (fileType === 'file') {
        this.textFiles = this.textFiles.filter(f => f.name !== fileName);
      }
    }
  };
}

// Start chat function for SPA - navigates to chat view instead of full page redirect
function startChatSPA(event) {
  if (event) {
    event.preventDefault();
  }

  const form = event ? event.target.closest('form') : document.querySelector('form');
  if (!form) return;

  const alpineComponent = form.closest('[x-data]');
  const select = alpineComponent ? alpineComponent.querySelector('select') : null;
  const textarea = form.querySelector('textarea');

  const selectedModel = select ? select.value : '';
  let message = textarea ? textarea.value : '';

  if (!message.trim() && window.currentPlaceholderText) {
    message = window.currentPlaceholderText;
  }

  if (!selectedModel || !message.trim()) {
    return;
  }

  // Get MCP mode from checkbox
  let mcpMode = false;
  const mcpToggle = document.getElementById('spa_home_mcp_toggle');
  if (mcpToggle && mcpToggle.checked) {
    mcpMode = true;
  }

  // Store message and files in localStorage for chat view to pick up
  const chatData = {
    message: message,
    imageFiles: [],
    audioFiles: [],
    textFiles: [],
    mcpMode: mcpMode
  };

  // Convert files to base64 for storage
  const imageInput = document.getElementById('spa_home_input_image');
  const audioInput = document.getElementById('spa_home_input_audio');
  const fileInput = document.getElementById('spa_home_input_file');

  const filePromises = [
    ...Array.from(imageInput?.files || []).map(file =>
      new Promise(resolve => {
        const reader = new FileReader();
        reader.onload = e => resolve({ name: file.name, data: e.target.result, type: file.type });
        reader.readAsDataURL(file);
      })
    ),
    ...Array.from(audioInput?.files || []).map(file =>
      new Promise(resolve => {
        const reader = new FileReader();
        reader.onload = e => resolve({ name: file.name, data: e.target.result, type: file.type });
        reader.readAsDataURL(file);
      })
    ),
    ...Array.from(fileInput?.files || []).map(file =>
      new Promise(resolve => {
        const reader = new FileReader();
        reader.onload = e => resolve({ name: file.name, data: e.target.result, type: file.type });
        reader.readAsText(file);
      })
    )
  ];

  const navigateToChat = () => {
    // Store in localStorage
    localStorage.setItem('localai_index_chat_data', JSON.stringify(chatData));

    // Use SPA router to navigate to chat
    if (window.Alpine && Alpine.store('router')) {
      Alpine.store('router').navigate('chat', { model: selectedModel });
    } else {
      // Fallback to full page redirect if router not available
      window.location.href = `/chat/${selectedModel}`;
    }
  };

  if (filePromises.length > 0) {
    Promise.all(filePromises).then(files => {
      files.forEach(file => {
        if (file.type.startsWith('image/')) {
          chatData.imageFiles.push(file);
        } else if (file.type.startsWith('audio/')) {
          chatData.audioFiles.push(file);
        } else {
          chatData.textFiles.push(file);
        }
      });
      navigateToChat();
    }).catch(err => {
      console.error('Error processing files:', err);
      navigateToChat();
    });
  } else {
    navigateToChat();
  }
}

// Resource Monitor component (GPU if available, otherwise RAM)
function resourceMonitor() {
  return {
    resourceData: null,
    pollInterval: null,

    async fetchResourceData() {
      try {
        const response = await fetch('/api/resources');
        if (response.ok) {
          this.resourceData = await response.json();
        }
      } catch (error) {
        console.error('Error fetching resource data:', error);
      }
    },

    startPolling() {
      this.fetchResourceData();
      this.pollInterval = setInterval(() => this.fetchResourceData(), 5000);
    },

    stopPolling() {
      if (this.pollInterval) {
        clearInterval(this.pollInterval);
      }
    }
  };
}

// Stop individual model
async function stopModel(modelName) {
  if (!confirm(`Are you sure you want to stop "${modelName}"?`)) {
    return;
  }

  try {
    const response = await fetch('/backend/shutdown', {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ model: modelName })
    });

    if (response.ok) {
      setTimeout(() => {
        window.location.reload();
      }, 500);
    } else {
      alert('Failed to stop model');
    }
  } catch (error) {
    console.error('Error stopping model:', error);
    alert('Failed to stop model');
  }
}

// Stop all loaded models
async function stopAllModels(component) {
  // Get loaded models from DOM
  const loadedModelElements = document.querySelectorAll('[data-loaded-model]');
  const loadedModelNames = Array.from(loadedModelElements).map(el => {
    const span = el.querySelector('span.truncate');
    return span ? span.textContent.trim() : '';
  }).filter(name => name.length > 0);

  if (loadedModelNames.length === 0) {
    return;
  }

  if (!confirm(`Are you sure you want to stop all ${loadedModelNames.length} loaded model(s)?`)) {
    return;
  }

  if (component) {
    component.stoppingAll = true;
  }

  try {
    const stopPromises = loadedModelNames.map(modelName =>
      fetch('/backend/shutdown', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify({ model: modelName })
      })
    );

    await Promise.all(stopPromises);

    setTimeout(() => {
      window.location.reload();
    }, 1000);
  } catch (error) {
    console.error('Error stopping models:', error);
    alert('Failed to stop some models');
    if (component) {
      component.stoppingAll = false;
    }
  }
}

// Make functions available globally
window.homeInputForm = homeInputForm;
window.startChatSPA = startChatSPA;
window.resourceMonitor = resourceMonitor;
window.stopModel = stopModel;
window.stopAllModels = stopAllModels;
