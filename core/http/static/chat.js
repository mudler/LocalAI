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

// Global variable to store the current AbortController
let currentAbortController = null;
let currentReader = null;
let requestStartTime = null;
let tokensReceived = 0;
let tokensPerSecondInterval = null;
let lastTokensPerSecond = null; // Store the last calculated rate

// Chat Storage Manager for handling multiple chats in localStorage
class ChatStorageManager {
  constructor() {
    this.STORAGE_KEY_CHATS = 'localai_chats';
    this.STORAGE_KEY_ACTIVE_CHAT = 'localai_active_chat_id';
    this.STORAGE_KEY_CACHE_LIMIT = 'localai_chat_cache_limit';
    this.DEFAULT_CACHE_LIMIT_MB = 50;
    this.DEFAULT_CACHE_LIMIT_CHATS = 100;
  }

  // Generate a unique chat ID
  generateChatId() {
    return 'chat_' + Date.now() + '_' + Math.random().toString(36).substr(2, 9);
  }

  // Get all chats from storage
  getAllChats() {
    try {
      const chatsJson = localStorage.getItem(this.STORAGE_KEY_CHATS);
      return chatsJson ? JSON.parse(chatsJson) : {};
    } catch (error) {
      console.error('Error loading chats from storage:', error);
      return {};
    }
  }

  // Save all chats to storage
  saveAllChats(chats) {
    try {
      localStorage.setItem(this.STORAGE_KEY_CHATS, JSON.stringify(chats));
      return true;
    } catch (error) {
      console.error('Error saving chats to storage:', error);
      // If quota exceeded, try to enforce cache limit
      if (error.name === 'QuotaExceededError') {
        this.enforceCacheLimit();
        // Try again after cleanup
        try {
          localStorage.setItem(this.STORAGE_KEY_CHATS, JSON.stringify(chats));
          return true;
        } catch (retryError) {
          console.error('Error saving after cache cleanup:', retryError);
          return false;
        }
      }
      return false;
    }
  }

  // Save a single chat
  saveChat(chatId, chatData) {
    const chats = this.getAllChats();
    chats[chatId] = {
      ...chatData,
      updatedAt: Date.now()
    };
    
    // Enforce cache limit before saving
    this.enforceCacheLimit();
    
    const saved = this.saveAllChats(chats);
    if (saved) {
      // Show warning if cleanup occurred
      const cleanedUp = this.enforceCacheLimit();
      if (cleanedUp > 0) {
        this.showToast(`Cleaned up ${cleanedUp} old chat(s) to free storage space`, 'warning');
      }
    }
    return saved;
  }

  // Load a specific chat
  loadChat(chatId) {
    const chats = this.getAllChats();
    return chats[chatId] || null;
  }

  // Delete a chat
  deleteChat(chatId) {
    const chats = this.getAllChats();
    if (chats[chatId]) {
      delete chats[chatId];
      this.saveAllChats(chats);
      
      // If deleted chat was active, clear active chat ID
      const activeChatId = this.getActiveChatId();
      if (activeChatId === chatId) {
        this.setActiveChatId(null);
      }
      return true;
    }
    return false;
  }

  // List all chats sorted by updatedAt (newest first), with stable secondary sort by createdAt
  listChats() {
    const chats = this.getAllChats();
    return Object.entries(chats)
      .map(([id, data]) => ({ id, ...data }))
      .sort((a, b) => {
        // Primary sort: updatedAt (newest first)
        const updatedDiff = (b.updatedAt || 0) - (a.updatedAt || 0);
        if (updatedDiff !== 0) return updatedDiff;
        // Secondary sort: createdAt (newest first) for stability
        return (b.createdAt || 0) - (a.createdAt || 0);
      });
  }

  // Get active chat ID
  getActiveChatId() {
    return localStorage.getItem(this.STORAGE_KEY_ACTIVE_CHAT);
  }

  // Set active chat ID
  setActiveChatId(chatId) {
    if (chatId) {
      localStorage.setItem(this.STORAGE_KEY_ACTIVE_CHAT, chatId);
    } else {
      localStorage.removeItem(this.STORAGE_KEY_ACTIVE_CHAT);
    }
  }

  // Calculate storage size in bytes
  getStorageSize() {
    const chats = this.getAllChats();
    const chatsJson = JSON.stringify(chats);
    return new Blob([chatsJson]).size;
  }

  // Get storage size in MB
  getStorageSizeMB() {
    return (this.getStorageSize() / (1024 * 1024)).toFixed(2);
  }

  // Get cache limit settings
  getCacheLimit() {
    const limitMB = localStorage.getItem(this.STORAGE_KEY_CACHE_LIMIT + '_mb');
    const limitChats = localStorage.getItem(this.STORAGE_KEY_CACHE_LIMIT + '_chats');
    return {
      mb: limitMB ? parseFloat(limitMB) : this.DEFAULT_CACHE_LIMIT_MB,
      chats: limitChats ? parseInt(limitChats) : this.DEFAULT_CACHE_LIMIT_CHATS
    };
  }

  // Set cache limit
  setCacheLimit(mb, chats) {
    if (mb !== null) localStorage.setItem(this.STORAGE_KEY_CACHE_LIMIT + '_mb', mb.toString());
    if (chats !== null) localStorage.setItem(this.STORAGE_KEY_CACHE_LIMIT + '_chats', chats.toString());
  }

  // Enforce cache limit - remove oldest chats if limit exceeded
  enforceCacheLimit() {
    const limit = this.getCacheLimit();
    const chats = this.listChats();
    let removedCount = 0;
    
    // Check chat count limit
    if (chats.length > limit.chats) {
      const toRemove = chats.slice(limit.chats);
      const chatsObj = this.getAllChats();
      toRemove.forEach(chat => {
        delete chatsObj[chat.id];
        removedCount++;
      });
      this.saveAllChats(chatsObj);
    }
    
    // Check storage size limit
    let currentSizeMB = parseFloat(this.getStorageSizeMB());
    while (currentSizeMB > limit.mb && chats.length > removedCount) {
      const chatsObj = this.getAllChats();
      const sortedChats = this.listChats();
      if (sortedChats.length === 0) break;
      
      // Remove oldest chat
      const oldestChat = sortedChats[sortedChats.length - 1];
      delete chatsObj[oldestChat.id];
      this.saveAllChats(chatsObj);
      removedCount++;
      
      currentSizeMB = parseFloat(this.getStorageSizeMB());
    }
    
    return removedCount;
  }

  // Clear all chats
  clearAllChats() {
    localStorage.removeItem(this.STORAGE_KEY_CHATS);
    localStorage.removeItem(this.STORAGE_KEY_ACTIVE_CHAT);
  }

  // Clear old chats (older than X days)
  clearOldChats(days) {
    const cutoffTime = Date.now() - (days * 24 * 60 * 60 * 1000);
    const chats = this.getAllChats();
    let removedCount = 0;
    
    Object.keys(chats).forEach(chatId => {
      const chat = chats[chatId];
      if ((chat.updatedAt || chat.createdAt || 0) < cutoffTime) {
        delete chats[chatId];
        removedCount++;
      }
    });
    
    if (removedCount > 0) {
      this.saveAllChats(chats);
    }
    
    return removedCount;
  }

  // Find or create chat for a model
  findOrCreateChatForModel(model) {
    const chats = this.listChats();
    // Find most recent chat for this model
    const existingChat = chats.find(chat => chat.model === model);
    
    if (existingChat) {
      return existingChat.id;
    }
    
    // Create new chat
    const newChatId = this.generateChatId();
    const newChat = {
      id: newChatId,
      model: model,
      title: `Chat with ${model}`,
      history: [],
      systemPrompt: '',
      mcpMode: false,
      tokenUsage: {
        promptTokens: 0,
        completionTokens: 0,
        totalTokens: 0,
        currentRequest: null
      },
      contextSize: null,
      createdAt: Date.now(),
      updatedAt: Date.now()
    };
    
    this.saveChat(newChatId, newChat);
    return newChatId;
  }

  // Generate chat title from first user message
  generateChatTitle(history) {
    if (!history || history.length === 0) {
      return 'New Chat';
    }
    
    // Find first user message
    const firstUserMessage = history.find(msg => msg.role === 'user');
    if (firstUserMessage) {
      const content = firstUserMessage.content || '';
      // Remove HTML tags and get first 50 characters
      const text = content.replace(/<[^>]*>/g, '').trim();
      return text.length > 50 ? text.substring(0, 50) + '...' : text || 'New Chat';
    }
    
    return 'New Chat';
  }

  // Show toast notification
  showToast(message, type = 'info') {
    // Create toast element if it doesn't exist
    let toastContainer = document.getElementById('toast-container');
    if (!toastContainer) {
      toastContainer = document.createElement('div');
      toastContainer.id = 'toast-container';
      toastContainer.className = 'fixed top-20 right-4 z-50 space-y-2';
      document.body.appendChild(toastContainer);
    }
    
    const toast = document.createElement('div');
    const bgColor = type === 'warning' ? 'bg-yellow-500/90' : type === 'error' ? 'bg-red-500/90' : 'bg-blue-500/90';
    toast.className = `${bgColor} text-white px-4 py-2 rounded-lg shadow-lg max-w-sm`;
    toast.textContent = message;
    
    toastContainer.appendChild(toast);
    
    // Remove toast after 3 seconds
    setTimeout(() => {
      toast.remove();
    }, 3000);
  }

  // Migrate old localStorage structure
  migrateOldData() {
    // Check for old system_prompt key
    const oldSystemPrompt = localStorage.getItem('system_prompt');
    if (oldSystemPrompt) {
      // Get current model from URL or page
      const modelElement = document.getElementById('chat-model');
      const model = modelElement ? modelElement.value : null;
      
      if (model) {
        // Create a chat with the old system prompt
        const chatId = this.findOrCreateChatForModel(model);
        const chat = this.loadChat(chatId);
        if (chat && !chat.systemPrompt) {
          chat.systemPrompt = oldSystemPrompt;
          this.saveChat(chatId, chat);
        }
        
        // Remove old key
        localStorage.removeItem('system_prompt');
      }
    }
  }
}

// Create global instance and expose it
const chatStorage = new ChatStorageManager();
window.chatStorage = chatStorage;

function toggleLoader(show) {
  const sendButton = document.getElementById('send-button');
  const stopButton = document.getElementById('stop-button');
  const headerLoadingIndicator = document.getElementById('header-loading-indicator');
  const tokensPerSecondDisplay = document.getElementById('tokens-per-second');
  
  if (show) {
    sendButton.style.display = 'none';
    stopButton.style.display = 'block';
    if (headerLoadingIndicator) headerLoadingIndicator.style.display = 'block';
    // Reset token tracking
    requestStartTime = Date.now();
    tokensReceived = 0;
    
    // Start updating tokens/second display
    if (tokensPerSecondDisplay) {
      tokensPerSecondDisplay.textContent = '-';
      updateTokensPerSecond();
      tokensPerSecondInterval = setInterval(updateTokensPerSecond, 500); // Update every 500ms
    }
  } else {
    sendButton.style.display = 'block';
    stopButton.style.display = 'none';
    if (headerLoadingIndicator) headerLoadingIndicator.style.display = 'none';
    // Stop updating but keep the last value visible
    if (tokensPerSecondInterval) {
      clearInterval(tokensPerSecondInterval);
      tokensPerSecondInterval = null;
    }
    // Keep the last calculated rate visible
    if (tokensPerSecondDisplay && lastTokensPerSecond !== null) {
      tokensPerSecondDisplay.textContent = lastTokensPerSecond;
    }
    currentAbortController = null;
    currentReader = null;
    requestStartTime = null;
    tokensReceived = 0;
  }
}

function updateTokensPerSecond() {
  const tokensPerSecondDisplay = document.getElementById('tokens-per-second');
  if (!tokensPerSecondDisplay || !requestStartTime) return;
  
  const elapsedSeconds = (Date.now() - requestStartTime) / 1000;
  if (elapsedSeconds > 0 && tokensReceived > 0) {
    const rate = tokensReceived / elapsedSeconds;
    const formattedRate = `${rate.toFixed(1)} tokens/s`;
    tokensPerSecondDisplay.textContent = formattedRate;
    lastTokensPerSecond = formattedRate; // Store the last calculated rate
  } else if (elapsedSeconds > 0) {
    tokensPerSecondDisplay.textContent = '-';
  }
}

function scrollThinkingBoxToBottom() {
  // Find all thinking/reasoning message containers that are expanded
  const thinkingBoxes = document.querySelectorAll('[data-thinking-box]');
  thinkingBoxes.forEach(box => {
    // Only scroll if the box is visible (expanded) and has overflow
    if (box.offsetParent !== null && box.scrollHeight > box.clientHeight) {
      box.scrollTo({
        top: box.scrollHeight,
        behavior: 'smooth'
      });
    }
  });
}

// Make function available globally
window.scrollThinkingBoxToBottom = scrollThinkingBoxToBottom;

function stopRequest() {
  if (currentAbortController) {
    currentAbortController.abort();
    currentAbortController = null;
  }
  if (currentReader) {
    currentReader.cancel();
    currentReader = null;
  }
  toggleLoader(false);
  Alpine.store("chat").add(
    "assistant",
    `<span class='error'>Request cancelled by user</span>`,
  );
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
  const systemPrompt = document.getElementById("systemPrompt").value;
  
  // Update Alpine store
  if (window.Alpine && Alpine.store("chat")) {
    Alpine.store("chat").systemPrompt = systemPrompt;
    // Save current chat immediately
    Alpine.store("chat").saveCurrentChat();
  }
  
  // Keep old localStorage for backward compatibility during migration
  localStorage.setItem("system_prompt", systemPrompt);
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
// Track file names to data URLs for proper removal
var imageFileMap = new Map(); // fileName -> dataURL
var audioFileMap = new Map(); // fileName -> dataURL

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

// Global function to handle file selection and update Alpine.js state
window.handleFileSelection = function(event, fileType) {
  if (!event.target.files || !event.target.files.length) return;
  
  // Get the Alpine.js component - find the parent div with x-data containing attachedFiles
  let inputContainer = event.target.closest('[x-data*="attachedFiles"]');
  if (!inputContainer && window.Alpine) {
    // Fallback: find any element with attachedFiles in x-data
    inputContainer = document.querySelector('[x-data*="attachedFiles"]');
  }
  if (!inputContainer || !window.Alpine) return;
  
  const alpineData = Alpine.$data(inputContainer);
  if (!alpineData || !alpineData.attachedFiles) return;
  
  Array.from(event.target.files).forEach(file => {
    // Check if file already exists
    const exists = alpineData.attachedFiles.some(f => f.name === file.name && f.type === fileType);
    if (!exists) {
      alpineData.attachedFiles.push({ name: file.name, type: fileType });
      
      // Process the file based on type
      if (fileType === 'image') {
        readInputImageFile(file);
      } else if (fileType === 'audio') {
        readInputAudioFile(file);
      } else if (fileType === 'file') {
        readInputFileFile(file);
      }
    }
  });
};

// Global function to remove file from input
window.removeFileFromInput = function(fileType, fileName) {
  // Remove from arrays
  if (fileType === 'image') {
    // Remove from images array using the mapping
    const dataURL = imageFileMap.get(fileName);
    if (dataURL) {
      const imageIndex = images.indexOf(dataURL);
      if (imageIndex !== -1) {
        images.splice(imageIndex, 1);
      }
      imageFileMap.delete(fileName);
    }
  } else if (fileType === 'audio') {
    // Remove from audios array using the mapping
    const dataURL = audioFileMap.get(fileName);
    if (dataURL) {
      const audioIndex = audios.indexOf(dataURL);
      if (audioIndex !== -1) {
        audios.splice(audioIndex, 1);
      }
      audioFileMap.delete(fileName);
    }
  } else if (fileType === 'file') {
    // Remove from fileContents and currentFileNames
    const fileIndex = currentFileNames.indexOf(fileName);
    if (fileIndex !== -1) {
      currentFileNames.splice(fileIndex, 1);
      fileContents.splice(fileIndex, 1);
    }
  }
  
  // Also remove from the actual input element
  const inputId = fileType === 'image' ? 'input_image' : 
                  fileType === 'audio' ? 'input_audio' : 'input_file';
  const input = document.getElementById(inputId);
  if (input && input.files) {
    const dt = new DataTransfer();
    Array.from(input.files).forEach(file => {
      if (file.name !== fileName) {
        dt.items.add(file);
      }
    });
    input.files = dt.files;
  }
};

function readInputFile() {
  if (!this.files || !this.files.length) return;

  Array.from(this.files).forEach(file => {
    readInputFileFile(file);
  });
}

function readInputFileFile(file) {
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
}

function submitPrompt(event) {
  event.preventDefault();
  
  const input = document.getElementById("input");
  if (!input) return;

  const inputValue = input.value;
  if (!inputValue.trim()) return; // Don't send empty messages

  // If already processing, abort the current request and send the new one
  if (currentAbortController || currentReader) {
    // Abort current request
    stopRequest();
    // Small delay to ensure cleanup completes
    setTimeout(() => {
      // Continue with new request
      processAndSendMessage(inputValue);
    }, 100);
    return;
  }
  
  processAndSendMessage(inputValue);
}

function processAndSendMessage(inputValue) {
  let fullInput = inputValue;
  
  // If there are file contents, append them to the input for the LLM
  if (fileContents.length > 0) {
    fullInput += "\n\nFile contents:\n";
    fileContents.forEach(file => {
      fullInput += `\n--- ${file.name} ---\n${file.content}\n`;
    });
  }
  
  // Show file icons in chat if there are files
  let displayContent = inputValue;
  if (currentFileNames.length > 0) {
    displayContent += "\n\n";
    currentFileNames.forEach(fileName => {
      displayContent += `<i class="fa-solid fa-file"></i> Attached file: ${fileName}\n`;
    });
  }
  
  // Save chat before sending first message if it's unsaved
  const store = Alpine.store("chat");
  if (store && store.isUnsavedChat) {
    store.saveCurrentChat();
  }
  
  // Add the message to the chat UI with just the icons
  store.add("user", displayContent, images, audios);
  
  // Update the last message in the store with the full content
  const history = Alpine.store("chat").history;
  if (history.length > 0) {
    history[history.length - 1].content = fullInput;
  }
  
  const input = document.getElementById("input");
  if (input) input.value = "";
  const systemPrompt = localStorage.getItem("system_prompt");
  Alpine.nextTick(() => {
    const chatContainer = document.getElementById('chat');
    if (chatContainer) {
      chatContainer.scrollTo({
        top: chatContainer.scrollHeight,
        behavior: 'smooth'
      });
    }
  });
  
  // Reset token tracking before starting new request
  requestStartTime = Date.now();
  tokensReceived = 0;
  
  promptGPT(systemPrompt, fullInput);
  
  // Reset file contents and names after sending
  fileContents = [];
  currentFileNames = [];
  images = [];
  audios = [];
  imageFileMap.clear();
  audioFileMap.clear();
  
  // Clear Alpine.js attachedFiles array
  const inputContainer = document.querySelector('[x-data*="attachedFiles"]');
  if (inputContainer && window.Alpine) {
    const alpineData = Alpine.$data(inputContainer);
    if (alpineData && alpineData.attachedFiles) {
      alpineData.attachedFiles = [];
    }
  }
  
  // Clear file inputs
  document.getElementById("input_image").value = null;
  document.getElementById("input_audio").value = null;
  document.getElementById("input_file").value = null;
}

function readInputImage() {
  if (!this.files || !this.files.length) return;

  Array.from(this.files).forEach(file => {
    readInputImageFile(file);
  });
}

function readInputImageFile(file) {
  const FR = new FileReader();

  FR.addEventListener("load", function(evt) {
    const dataURL = evt.target.result;
    images.push(dataURL);
    imageFileMap.set(file.name, dataURL);
  });

  FR.readAsDataURL(file);
}

function readInputAudio() {
  if (!this.files || !this.files.length) return;

  Array.from(this.files).forEach(file => {
    readInputAudioFile(file);
  });
}

function readInputAudioFile(file) {
  const FR = new FileReader();

  FR.addEventListener("load", function(evt) {
    const dataURL = evt.target.result;
    audios.push(dataURL);
    audioFileMap.set(file.name, dataURL);
  });

  FR.readAsDataURL(file);
}

async function promptGPT(systemPrompt, input) {
  const model = document.getElementById("chat-model").value;
  const mcpMode = Alpine.store("chat").mcpMode;
  
  // Reset current request usage tracking for new request
  if (Alpine.store("chat")) {
    Alpine.store("chat").tokenUsage.currentRequest = null;
  }
  
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

  // reset the form and the files (already done in processAndSendMessage)
  // images, audios, and file inputs are cleared after sending

  // Choose endpoint based on MCP mode
  const endpoint = mcpMode ? "v1/mcp/chat/completions" : "v1/chat/completions";
  const requestBody = {
    model: model,
    messages: messages,
  };
  
  // Add stream parameter for both regular chat and MCP (MCP now supports SSE streaming)
  requestBody.stream = true;
  
  let response;
  try {
    // Create AbortController for timeout handling and stop button
    const controller = new AbortController();
    currentAbortController = controller; // Store globally so stop button can abort it
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
    // Don't show error if request was aborted by user (stop button)
    if (error.name === 'AbortError') {
      // Check if this was a user-initiated abort (stop button was clicked)
      // If currentAbortController is null, it means stopRequest() was called and already handled the UI
      if (!currentAbortController) {
        // User clicked stop button - error message already shown by stopRequest()
        return;
      } else {
        // Timeout error (controller was aborted by timeout, not user)
        Alpine.store("chat").add(
          "assistant",
          `<span class='error'>Request timeout: MCP processing is taking longer than expected. Please try again.</span>`,
        );
      }
    } else {
      Alpine.store("chat").add(
        "assistant",
        `<span class='error'>Network Error: ${error.message}</span>`,
      );
    }
    toggleLoader(false);
    currentAbortController = null;
    return;
  }

  if (!response.ok) {
    Alpine.store("chat").add(
      "assistant",
      `<span class='error'>Error: POST ${endpoint} ${response.status}</span>`,
    );
    toggleLoader(false);
    currentAbortController = null;
    return;
  }

  // Handle streaming response (both regular and MCP mode now use SSE)
  if (mcpMode) {
    // Handle MCP SSE streaming with new event types
    const reader = response.body
      ?.pipeThrough(new TextDecoderStream())
      .getReader();

    if (!reader) {
      Alpine.store("chat").add(
        "assistant",
        `<span class='error'>Error: Failed to decode MCP API response</span>`,
      );
      toggleLoader(false);
      return;
    }

    // Store reader globally so stop button can cancel it
    currentReader = reader;

    let buffer = "";
    let assistantContent = "";
    let assistantContentBuffer = [];
    let thinkingContent = "";
    let isThinking = false;
    let lastAssistantMessageIndex = -1;
    let lastThinkingMessageIndex = -1;
    let lastThinkingScrollTime = 0;
    const THINKING_SCROLL_THROTTLE = 200; // Throttle scrolling to every 200ms

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
              const eventData = JSON.parse(line.substring(6));
              
              // Handle different event types
              switch (eventData.type) {
                case "reasoning":
                  if (eventData.content) {
                    const chatStore = Alpine.store("chat");
                    // Insert reasoning before assistant message if it exists
                    if (lastAssistantMessageIndex >= 0 && chatStore.history[lastAssistantMessageIndex]?.role === "assistant") {
                      chatStore.history.splice(lastAssistantMessageIndex, 0, {
                        role: "reasoning",
                        content: eventData.content,
                        html: DOMPurify.sanitize(marked.parse(eventData.content)),
                        image: [],
                        audio: [],
                        expanded: false // Reasoning is always collapsed
                      });
                      lastAssistantMessageIndex++; // Adjust index since we inserted
                      // Scroll smoothly after adding reasoning
                      setTimeout(() => {
                        const chatContainer = document.getElementById('chat');
                        if (chatContainer) {
                          chatContainer.scrollTo({
                            top: chatContainer.scrollHeight,
                            behavior: 'smooth'
                          });
                        }
                      }, 100);
                    } else {
                      // No assistant message yet, just add normally
                      chatStore.add("reasoning", eventData.content);
                    }
                  }
                  break;
                
                case "tool_call":
                  if (eventData.name) {
                    // Store as JSON for better formatting
                    const toolCallData = {
                      name: eventData.name,
                      arguments: eventData.arguments || {},
                      reasoning: eventData.reasoning || ""
                    };
                    Alpine.store("chat").add("tool_call", JSON.stringify(toolCallData, null, 2));
                    // Scroll smoothly after adding tool call
                    setTimeout(() => {
                      const chatContainer = document.getElementById('chat');
                      if (chatContainer) {
                        chatContainer.scrollTo({
                          top: chatContainer.scrollHeight,
                          behavior: 'smooth'
                        });
                      }
                    }, 100);
                  }
                  break;
                
                case "tool_result":
                  if (eventData.name) {
                    // Store as JSON for better formatting
                    const toolResultData = {
                      name: eventData.name,
                      result: eventData.result || ""
                    };
                    Alpine.store("chat").add("tool_result", JSON.stringify(toolResultData, null, 2));
                    // Scroll smoothly after adding tool result
                    setTimeout(() => {
                      const chatContainer = document.getElementById('chat');
                      if (chatContainer) {
                        chatContainer.scrollTo({
                          top: chatContainer.scrollHeight,
                          behavior: 'smooth'
                        });
                      }
                    }, 100);
                  }
                  break;
                
                case "status":
                  // Status messages can be logged but not necessarily displayed
                  console.log("[MCP Status]", eventData.message);
                  break;
                
                case "assistant":
                  if (eventData.content) {
                    assistantContent += eventData.content;
                    const contentChunk = eventData.content;
                    
                    // Count tokens for rate calculation
                    tokensReceived += Math.ceil(contentChunk.length / 4);
                    updateTokensPerSecond();
                    
                    // Check for thinking tags in the chunk (incremental detection)
                    if (contentChunk.includes("<thinking>") || contentChunk.includes("<think>")) {
                      isThinking = true;
                      thinkingContent = "";
                      lastThinkingMessageIndex = -1;
                    }
                    
                    if (contentChunk.includes("</thinking>") || contentChunk.includes("</think>")) {
                      isThinking = false;
                      // When closing tag is detected, process the accumulated thinking content
                      if (thinkingContent.trim()) {
                        // Extract just the thinking part from the accumulated content
                        const thinkingMatch = thinkingContent.match(/<(?:thinking|redacted_reasoning)>(.*?)<\/(?:thinking|redacted_reasoning)>/s);
                        if (thinkingMatch && thinkingMatch[1]) {
                          const extractedThinking = thinkingMatch[1];
                          const chatStore = Alpine.store("chat");
                          const isMCPMode = chatStore.mcpMode || false;
                          const shouldExpand = !isMCPMode; // Expanded in non-MCP mode, collapsed in MCP mode
                          if (lastThinkingMessageIndex === -1) {
                            // Insert thinking before the last assistant message if it exists
                            if (lastAssistantMessageIndex >= 0 && chatStore.history[lastAssistantMessageIndex]?.role === "assistant") {
                              // Insert before assistant message
                              chatStore.history.splice(lastAssistantMessageIndex, 0, {
                                role: "thinking",
                                content: extractedThinking,
                                html: DOMPurify.sanitize(marked.parse(extractedThinking)),
                                image: [],
                                audio: [],
                                expanded: shouldExpand
                              });
                              lastThinkingMessageIndex = lastAssistantMessageIndex;
                              lastAssistantMessageIndex++; // Adjust index since we inserted
                            } else {
                              // No assistant message yet, just add normally
                              chatStore.add("thinking", extractedThinking);
                              lastThinkingMessageIndex = chatStore.history.length - 1;
                            }
                          } else {
                            // Update existing thinking message
                            const lastMessage = chatStore.history[lastThinkingMessageIndex];
                            if (lastMessage && lastMessage.role === "thinking") {
                              lastMessage.content = extractedThinking;
                              lastMessage.html = DOMPurify.sanitize(marked.parse(extractedThinking));
                            }
                          }
                          // Scroll when thinking is finalized in non-MCP mode
                          if (!isMCPMode) {
                            setTimeout(() => {
                              const chatContainer = document.getElementById('chat');
                              if (chatContainer) {
                                chatContainer.scrollTo({
                                  top: chatContainer.scrollHeight,
                                  behavior: 'smooth'
                                });
                              }
                            }, 50);
                          }
                        }
                        thinkingContent = "";
                      }
                    }
                    
                    // Handle content based on thinking state
                    if (isThinking) {
                      thinkingContent += contentChunk;
                      const chatStore = Alpine.store("chat");
                      const isMCPMode = chatStore.mcpMode || false;
                      const shouldExpand = !isMCPMode; // Expanded in non-MCP mode, collapsed in MCP mode
                      // Update the last thinking message or create a new one (incremental)
                      if (lastThinkingMessageIndex === -1) {
                        // Insert thinking before the last assistant message if it exists
                        if (lastAssistantMessageIndex >= 0 && chatStore.history[lastAssistantMessageIndex]?.role === "assistant") {
                          // Insert before assistant message
                          chatStore.history.splice(lastAssistantMessageIndex, 0, {
                            role: "thinking",
                            content: thinkingContent,
                            html: DOMPurify.sanitize(marked.parse(thinkingContent)),
                            image: [],
                            audio: [],
                            expanded: shouldExpand
                          });
                          lastThinkingMessageIndex = lastAssistantMessageIndex;
                          lastAssistantMessageIndex++; // Adjust index since we inserted
                        } else {
                          // No assistant message yet, just add normally
                          chatStore.add("thinking", thinkingContent);
                          lastThinkingMessageIndex = chatStore.history.length - 1;
                        }
                      } else {
                        // Update existing thinking message
                        const lastMessage = chatStore.history[lastThinkingMessageIndex];
                        if (lastMessage && lastMessage.role === "thinking") {
                          lastMessage.content = thinkingContent;
                          lastMessage.html = DOMPurify.sanitize(marked.parse(thinkingContent));
                        }
                      }
                      // Scroll when thinking is updated in non-MCP mode (throttled)
                      if (!isMCPMode) {
                        const now = Date.now();
                        if (now - lastThinkingScrollTime > THINKING_SCROLL_THROTTLE) {
                          lastThinkingScrollTime = now;
                          setTimeout(() => {
                            const chatContainer = document.getElementById('chat');
                            if (chatContainer) {
                              chatContainer.scrollTo({
                                top: chatContainer.scrollHeight,
                                behavior: 'smooth'
                              });
                            }
                          }, 100);
                        }
                      }
                    } else {
                      // Regular assistant content - buffer it for batch processing
                      assistantContentBuffer.push(contentChunk);
                    }
                  }
                  break;
                
                case "error":
                  Alpine.store("chat").add(
                    "assistant",
                    `<span class='error'>MCP Error: ${eventData.message}</span>`,
                  );
                  break;
              }
            } catch (error) {
              console.error("Failed to parse MCP event:", line, error);
            }
          }
        });
        
        // Efficiently update assistant message in batch
        if (assistantContentBuffer.length > 0) {
          const regularContent = assistantContentBuffer.join("");
          
          // Process any thinking tags that might be in the accumulated content
          // This handles cases where tags are split across chunks
          const { regularContent: processedRegular, thinkingContent: processedThinking } = processThinkingTags(regularContent);
          
          // Update or create assistant message with processed regular content
          if (lastAssistantMessageIndex === -1) {
            if (processedRegular && processedRegular.trim()) {
              Alpine.store("chat").add("assistant", processedRegular);
              lastAssistantMessageIndex = Alpine.store("chat").history.length - 1;
            }
          } else {
            const chatStore = Alpine.store("chat");
            const lastMessage = chatStore.history[lastAssistantMessageIndex];
            if (lastMessage && lastMessage.role === "assistant") {
              lastMessage.content = (lastMessage.content || "") + (processedRegular || "");
              lastMessage.html = DOMPurify.sanitize(marked.parse(lastMessage.content));
            }
          }
          
          // Add any extracted thinking content from the processed buffer BEFORE assistant message
          if (processedThinking && processedThinking.trim()) {
            const chatStore = Alpine.store("chat");
            const isMCPMode = chatStore.mcpMode || false;
            const shouldExpand = !isMCPMode; // Expanded in non-MCP mode, collapsed in MCP mode
            // Insert thinking before assistant message if it exists
            if (lastAssistantMessageIndex >= 0 && chatStore.history[lastAssistantMessageIndex]?.role === "assistant") {
              chatStore.history.splice(lastAssistantMessageIndex, 0, {
                role: "thinking",
                content: processedThinking,
                html: DOMPurify.sanitize(marked.parse(processedThinking)),
                image: [],
                audio: [],
                expanded: shouldExpand
              });
              lastAssistantMessageIndex++; // Adjust index since we inserted
            } else {
              // No assistant message yet, just add normally
              chatStore.add("thinking", processedThinking);
            }
          }
          
          assistantContentBuffer = [];
        }
      }

      // Final assistant content flush if any data remains
      if (assistantContentBuffer.length > 0) {
        const regularContent = assistantContentBuffer.join("");
        // Process any remaining thinking tags that might be in the buffer
        const { regularContent: processedRegular, thinkingContent: processedThinking } = processThinkingTags(regularContent);
        
        const chatStore = Alpine.store("chat");
        
        // First, add any extracted thinking content BEFORE assistant message
        if (processedThinking && processedThinking.trim()) {
          const isMCPMode = chatStore.mcpMode || false;
          const shouldExpand = !isMCPMode; // Expanded in non-MCP mode, collapsed in MCP mode
          // Insert thinking before assistant message if it exists
          if (lastAssistantMessageIndex >= 0 && chatStore.history[lastAssistantMessageIndex]?.role === "assistant") {
            chatStore.history.splice(lastAssistantMessageIndex, 0, {
              role: "thinking",
              content: processedThinking,
              html: DOMPurify.sanitize(marked.parse(processedThinking)),
              image: [],
              audio: [],
              expanded: shouldExpand
            });
            lastAssistantMessageIndex++; // Adjust index since we inserted
          } else {
            // No assistant message yet, just add normally
            chatStore.add("thinking", processedThinking);
          }
        }
        
        // Then update or create assistant message
        if (lastAssistantMessageIndex !== -1) {
          const lastMessage = chatStore.history[lastAssistantMessageIndex];
          if (lastMessage && lastMessage.role === "assistant") {
            lastMessage.content = (lastMessage.content || "") + (processedRegular || "");
            lastMessage.html = DOMPurify.sanitize(marked.parse(lastMessage.content));
          }
        } else if (processedRegular && processedRegular.trim()) {
          chatStore.add("assistant", processedRegular);
          lastAssistantMessageIndex = chatStore.history.length - 1;
        }
      }
      
      // Final thinking content flush if any data remains (from incremental detection)
      if (thinkingContent.trim() && lastThinkingMessageIndex === -1) {
        // Extract thinking content if tags are present
        const thinkingMatch = thinkingContent.match(/<(?:thinking|redacted_reasoning)>(.*?)<\/(?:thinking|redacted_reasoning)>/s);
        if (thinkingMatch && thinkingMatch[1]) {
          const chatStore = Alpine.store("chat");
          const isMCPMode = chatStore.mcpMode || false;
          const shouldExpand = !isMCPMode; // Expanded in non-MCP mode, collapsed in MCP mode
          // Insert thinking before assistant message if it exists
          if (lastAssistantMessageIndex >= 0 && chatStore.history[lastAssistantMessageIndex]?.role === "assistant") {
            chatStore.history.splice(lastAssistantMessageIndex, 0, {
              role: "thinking",
              content: thinkingMatch[1],
              html: DOMPurify.sanitize(marked.parse(thinkingMatch[1])),
              image: [],
              audio: [],
              expanded: shouldExpand
            });
          } else {
            // No assistant message yet, just add normally
            chatStore.add("thinking", thinkingMatch[1]);
          }
        } else {
          Alpine.store("chat").add("thinking", thinkingContent);
        }
      }
      
      // Final pass: process the entire assistantContent to catch any missed thinking tags
      // This ensures we don't miss tags that were split across chunks
      if (assistantContent.trim()) {
        const { regularContent: finalRegular, thinkingContent: finalThinking } = processThinkingTags(assistantContent);
        
        // Update assistant message with final processed content (without thinking tags)
        if (finalRegular && finalRegular.trim()) {
          if (lastAssistantMessageIndex !== -1) {
            const chatStore = Alpine.store("chat");
            const lastMessage = chatStore.history[lastAssistantMessageIndex];
            if (lastMessage && lastMessage.role === "assistant") {
              lastMessage.content = finalRegular;
              lastMessage.html = DOMPurify.sanitize(marked.parse(lastMessage.content));
            }
          } else {
            Alpine.store("chat").add("assistant", finalRegular);
          }
        }
        
        // Add any extracted thinking content (only if not already added)
        if (finalThinking && finalThinking.trim()) {
          const hasThinking = Alpine.store("chat").history.some(msg => 
            msg.role === "thinking" && msg.content.trim() === finalThinking.trim()
          );
          if (!hasThinking) {
            Alpine.store("chat").add("thinking", finalThinking);
          }
        }
      }

      // Highlight all code blocks once at the end
      hljs.highlightAll();
    } catch (error) {
      // Don't show error if request was aborted by user
      if (error.name !== 'AbortError' || !currentAbortController) {
        Alpine.store("chat").add(
          "assistant",
          `<span class='error'>Error: Failed to process MCP stream</span>`,
        );
      }
    } finally {
      // Perform any cleanup if necessary
      if (reader) {
        reader.releaseLock();
      }
      currentReader = null;
      currentAbortController = null;
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
      toggleLoader(false);
      return;
    }

    // Store reader globally so stop button can cancel it
    currentReader = reader;

    // Function to add content to the chat and handle DOM updates efficiently
    const addToChat = (token) => {
      const chatStore = Alpine.store("chat");
      chatStore.add("assistant", token);
      // Count tokens for rate calculation (rough estimate: count characters/4)
      tokensReceived += Math.ceil(token.length / 4);
      updateTokensPerSecond();
      // Efficiently scroll into view without triggering multiple reflows
      // const messages = document.getElementById('messages');
      // messages.scrollTop = messages.scrollHeight;
    };

    let buffer = "";
    let contentBuffer = [];
    let thinkingContent = "";
    let isThinking = false;
    let lastThinkingMessageIndex = -1;
    let lastThinkingScrollTime = 0;
    const THINKING_SCROLL_THROTTLE = 200; // Throttle scrolling to every 200ms

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
              
              // Update token usage if present
              if (jsonData.usage) {
                Alpine.store("chat").updateTokenUsage(jsonData.usage);
              }
              
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
                  // Count tokens for rate calculation
                  tokensReceived += Math.ceil(token.length / 4);
                  updateTokensPerSecond();
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
                  // Scroll when thinking is updated (throttled)
                  const now = Date.now();
                  if (now - lastThinkingScrollTime > THINKING_SCROLL_THROTTLE) {
                    lastThinkingScrollTime = now;
                    setTimeout(() => {
                      // Scroll main chat container
                      const chatContainer = document.getElementById('chat');
                      if (chatContainer) {
                        chatContainer.scrollTo({
                          top: chatContainer.scrollHeight,
                          behavior: 'smooth'
                        });
                      }
                      // Scroll thinking box to bottom if it's expanded and scrollable
                      scrollThinkingBoxToBottom();
                    }, 100);
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
          // Scroll when assistant content is updated (this will also show thinking messages above)
          setTimeout(() => {
            const chatContainer = document.getElementById('chat');
            if (chatContainer) {
              chatContainer.scrollTo({
                top: chatContainer.scrollHeight,
                behavior: 'smooth'
              });
            }
          }, 50);
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
      // Don't show error if request was aborted by user
      if (error.name !== 'AbortError' || !currentAbortController) {
        Alpine.store("chat").add(
          "assistant",
          `<span class='error'>Error: Failed to process stream</span>`,
        );
      }
    } finally {
      // Perform any cleanup if necessary
      if (reader) {
        reader.releaseLock();
      }
      currentReader = null;
      currentAbortController = null;
    }
  }

  // Remove class "loader" from the element with "loader" id
  toggleLoader(false);

  // scroll to the bottom of the chat consistently
  setTimeout(() => {
    const chatContainer = document.getElementById('chat');
    if (chatContainer) {
      chatContainer.scrollTo({
        top: chatContainer.scrollHeight,
        behavior: 'smooth'
      });
    }
  }, 100);
  
  // set focus to the input
  document.getElementById("input").focus();
}

// Wait for DOM and Alpine to be ready before initializing
document.addEventListener('DOMContentLoaded', function() {
  // Wait for Alpine to be ready
  setTimeout(() => {
    initializeChatStorage();
  }, 300);
});

function initializeChatStorage() {
  // Migrate old data first
  chatStorage.migrateOldData();
  
  // Get model from URL or page
  const urlParams = new URLSearchParams(window.location.search);
  const chatIdFromUrl = urlParams.get('chatId');
  const modelElement = document.getElementById('chat-model');
  const model = modelElement ? modelElement.value : null;
  
  if (!window.Alpine || !Alpine.store("chat")) {
    console.warn('Alpine store not available, retrying...');
    setTimeout(initializeChatStorage, 500);
    return;
  }
  
  const store = Alpine.store("chat");
  
  // Set initial model from page
  if (model && !store.currentModel) {
    store.currentModel = model;
  }
  
  // Check MCP availability for initial model
  if (store.currentModel) {
    store.checkMCPAvailability();
  }
  
  // Refresh chat list
  store.refreshChatList();
  
  // Check if we have a chatId in URL
  if (chatIdFromUrl) {
    const loaded = store.loadChat(chatIdFromUrl);
    if (!loaded) {
      // Chat not found, create new one
      if (model) {
        store.createNewChat(model);
      }
    }
  } else {
    // No chatId in URL, check for active chat or create new
    const activeChatId = chatStorage.getActiveChatId();
    if (activeChatId && chatStorage.loadChat(activeChatId)) {
      store.loadChat(activeChatId);
    } else if (model) {
      // Create new temporary chat for current model (don't save until user sends message)
      store.createNewChat(model, false);
    }
  }
  
  // Set up auto-save on page unload
  window.addEventListener('beforeunload', function() {
    if (store.currentChatId) {
      // Save immediately (no debounce on unload)
      store.saveCurrentChat();
    }
  });
  
  // Also save when system prompt changes (immediate, not debounced)
  const systemPromptElement = document.getElementById('systemPrompt');
  if (systemPromptElement) {
    systemPromptElement.addEventListener('input', function() {
      if (store.currentChatId) {
        store.systemPrompt = this.value;
        // Debounced save for input events
        store.saveCurrentChatDebounced();
      }
    });
  }
  
  // Load system prompt from current chat or old storage
  const storesystemPrompt = store.systemPrompt || localStorage.getItem("system_prompt");
  if (storesystemPrompt && systemPromptElement) {
    systemPromptElement.value = storesystemPrompt;
    store.systemPrompt = storesystemPrompt;
  }
}

// Set up event listeners
document.addEventListener('DOMContentLoaded', function() {
  const systemPromptForm = document.getElementById("system_prompt");
  const promptForm = document.getElementById("prompt");
  const inputElement = document.getElementById("input");
  
  if (systemPromptForm) {
    systemPromptForm.addEventListener("submit", submitSystemPrompt);
  }
  if (promptForm) {
    promptForm.addEventListener("submit", submitPrompt);
  }
  if (inputElement) {
    inputElement.focus();
  }
});

marked.setOptions({
  highlight: function (code) {
    return hljs.highlightAuto(code).value;
  },
});

// Alpine store is now initialized in chat.html inline script to ensure it's available before Alpine processes the DOM
// Only initialize if not already initialized (to avoid duplicate initialization)
document.addEventListener("alpine:init", () => {
  // Check if store already exists (initialized in chat.html)
  if (!Alpine.store("chat")) {
    // Fallback initialization (should not be needed if chat.html loads correctly)
    Alpine.store("chat", {
    history: [],
    languages: [undefined],
    systemPrompt: "",
    mcpMode: false,
    contextSize: null,
    tokenUsage: {
      promptTokens: 0,
      completionTokens: 0,
      totalTokens: 0,
      currentRequest: null
    },
    clear() {
      this.history.length = 0;
      this.tokenUsage = {
        promptTokens: 0,
        completionTokens: 0,
        totalTokens: 0,
        currentRequest: null
      };
    },
    updateTokenUsage(usage) {
      // Usage values in streaming responses are cumulative totals for the current request
      // We track session totals separately and only update when we see new (higher) values
      if (usage) {
        const currentRequest = this.tokenUsage.currentRequest || {
          promptTokens: 0,
          completionTokens: 0,
          totalTokens: 0
        };
        
        // Check if this is a new/updated usage (values increased)
        const isNewUsage = 
          (usage.prompt_tokens !== undefined && usage.prompt_tokens > currentRequest.promptTokens) ||
          (usage.completion_tokens !== undefined && usage.completion_tokens > currentRequest.completionTokens) ||
          (usage.total_tokens !== undefined && usage.total_tokens > currentRequest.totalTokens);
        
        if (isNewUsage) {
          // Update session totals: subtract old request usage, add new
          this.tokenUsage.promptTokens = this.tokenUsage.promptTokens - currentRequest.promptTokens + (usage.prompt_tokens || 0);
          this.tokenUsage.completionTokens = this.tokenUsage.completionTokens - currentRequest.completionTokens + (usage.completion_tokens || 0);
          this.tokenUsage.totalTokens = this.tokenUsage.totalTokens - currentRequest.totalTokens + (usage.total_tokens || 0);
          
          // Store current request usage
          this.tokenUsage.currentRequest = {
            promptTokens: usage.prompt_tokens || 0,
            completionTokens: usage.completion_tokens || 0,
            totalTokens: usage.total_tokens || 0
          };
        }
      }
    },
    getRemainingTokens() {
      if (!this.contextSize) return null;
      return Math.max(0, this.contextSize - this.tokenUsage.totalTokens);
    },
    getContextUsagePercent() {
      if (!this.contextSize) return null;
      return Math.min(100, (this.tokenUsage.totalTokens / this.contextSize) * 100);
    },
    add(role, content, image, audio) {
      const N = this.history.length - 1;
      // For thinking and reasoning messages, always create a new message
      if (role === "thinking" || role === "reasoning") {
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
      const chatContainer = document.getElementById('chat');
      if (chatContainer) {
        chatContainer.scrollTo({
          top: chatContainer.scrollHeight,
          behavior: 'smooth'
        });
      }
      // Also scroll thinking box if it's a thinking/reasoning message
      if (role === "thinking" || role === "reasoning") {
        setTimeout(() => {
          if (typeof window.scrollThinkingBoxToBottom === 'function') {
            window.scrollThinkingBoxToBottom();
          }
        }, 100);
      }
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
  }
});

// Check for message from index page on load
document.addEventListener('DOMContentLoaded', function() {
  // Wait for Alpine and chat storage to be ready
  setTimeout(() => {
    const chatData = localStorage.getItem('localai_index_chat_data');
    if (chatData) {
      try {
        const data = JSON.parse(chatData);
        
        // Get model from URL or page
        const modelElement = document.getElementById('chat-model');
        const model = modelElement ? modelElement.value : null;
        
        if (model && window.Alpine && Alpine.store("chat")) {
          const store = Alpine.store("chat");
          
          // Create new chat for this model
          const chatId = store.createNewChat(model);
          
          // Set MCP mode if provided
          if (data.mcpMode === true) {
            store.mcpMode = true;
            store.saveCurrentChat();
          }
          
          const input = document.getElementById('input');
          
          if (input && data.message) {
            // Set the message in the input
            input.value = data.message;
            
            // Process files if any
            if (data.imageFiles && data.imageFiles.length > 0) {
              data.imageFiles.forEach(file => {
                images.push(file.data);
              });
            }
            
            if (data.audioFiles && data.audioFiles.length > 0) {
              data.audioFiles.forEach(file => {
                audios.push(file.data);
              });
            }
            
            if (data.textFiles && data.textFiles.length > 0) {
              data.textFiles.forEach(file => {
                fileContents.push({ name: file.name, content: file.data });
                currentFileNames.push(file.name);
              });
            }
            
            // Clear localStorage
            localStorage.removeItem('localai_index_chat_data');
            
            // Auto-submit after a short delay to ensure everything is ready
            setTimeout(() => {
              if (input.value.trim()) {
                processAndSendMessage(input.value);
              }
            }, 500);
          } else {
            // No message, but might have mcpMode - clear localStorage
            localStorage.removeItem('localai_index_chat_data');
          }
        } else {
          // Fallback: clear localStorage if store not ready
          localStorage.removeItem('localai_index_chat_data');
        }
      } catch (error) {
        console.error('Error processing chat data from index:', error);
        localStorage.removeItem('localai_index_chat_data');
      }
    }
  }, 500);
});

