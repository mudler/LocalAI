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

// Track requests per chat ID to support parallel chatting
let activeRequests = new Map(); // chatId -> { controller, reader, startTime, tokensReceived, interval, maxTokensPerSecond }

// Global variables for UI (stop button, etc.)
let currentAbortController = null; // For stop button - tracks the active chat's request
let currentReader = null;
let tokensPerSecondInterval = null;
let tokensPerSecondIntervalChatId = null; // Track which chat the interval is for
let lastTokensPerSecond = null; // Store the last calculated rate

// Storage key for chats
const CHATS_STORAGE_KEY = 'localai_chats_data';
const SYSTEM_PROMPT_STORAGE_KEY = 'system_prompt'; // Old key for migration

// Debounce timer for auto-save
let saveDebounceTimer = null;
const SAVE_DEBOUNCE_MS = 500;

// Save chats to localStorage with error handling
function saveChatsToStorage() {
  if (!window.Alpine || !Alpine.store("chat")) {
    return false;
  }
  
  try {
    const chatStore = Alpine.store("chat");
    const data = {
      chats: chatStore.chats.map(chat => ({
        id: chat.id,
        name: chat.name,
        model: chat.model,
        history: chat.history,
        systemPrompt: chat.systemPrompt,
        mcpMode: chat.mcpMode,
        tokenUsage: chat.tokenUsage,
        contextSize: chat.contextSize,
        createdAt: chat.createdAt,
        updatedAt: chat.updatedAt
      })),
      activeChatId: chatStore.activeChatId,
      lastSaved: Date.now()
    };
    
    const jsonData = JSON.stringify(data);
    localStorage.setItem(CHATS_STORAGE_KEY, jsonData);
    return true;
  } catch (error) {
    // Handle quota exceeded or other storage errors
    if (error.name === 'QuotaExceededError' || error.code === 22) {
      console.warn('localStorage quota exceeded. Consider cleaning up old chats.');
      // Try to save without history (last resort)
      try {
        const chatStore = Alpine.store("chat");
        const data = {
          chats: chatStore.chats.map(chat => ({
            id: chat.id,
            name: chat.name,
            model: chat.model,
            history: [], // Clear history to save space
            systemPrompt: chat.systemPrompt,
            mcpMode: chat.mcpMode,
            tokenUsage: chat.tokenUsage,
            contextSize: chat.contextSize,
            createdAt: chat.createdAt,
            updatedAt: chat.updatedAt
          })),
          activeChatId: chatStore.activeChatId,
          lastSaved: Date.now()
        };
        localStorage.setItem(CHATS_STORAGE_KEY, JSON.stringify(data));
        return true;
      } catch (e2) {
        console.error('Failed to save chats even without history:', e2);
        return false;
      }
    } else {
      console.error('Error saving chats to localStorage:', error);
      return false;
    }
  }
}

// Load chats from localStorage with migration support
function loadChatsFromStorage() {
  try {
    const stored = localStorage.getItem(CHATS_STORAGE_KEY);
    if (stored) {
      const data = JSON.parse(stored);
      
      // Validate structure
      if (data && Array.isArray(data.chats)) {
        return {
          chats: data.chats,
          activeChatId: data.activeChatId || null,
          lastSaved: data.lastSaved || null
        };
      }
    }
    
    // Migration: Check for old format
    const oldSystemPrompt = localStorage.getItem(SYSTEM_PROMPT_STORAGE_KEY);
    if (oldSystemPrompt) {
      // Migrate old single-chat format to new multi-chat format
      const chatStore = Alpine.store("chat");
      if (chatStore) {
        const migratedChat = chatStore.createChat(
          document.getElementById("chat-model")?.value || "",
          oldSystemPrompt,
          false
        );
        // Try to preserve any existing history if available
        if (chatStore.activeChat()) {
          chatStore.activeChat().name = "Migrated Chat";
        }
        // Save migrated data
        saveChatsToStorage();
        // Remove old key
        localStorage.removeItem(SYSTEM_PROMPT_STORAGE_KEY);
        return {
          chats: chatStore.chats,
          activeChatId: chatStore.activeChatId,
          lastSaved: Date.now()
        };
      }
    }
    
    return null;
  } catch (error) {
    console.error('Error loading chats from localStorage:', error);
    // Try to recover by clearing corrupted data
    try {
      localStorage.removeItem(CHATS_STORAGE_KEY);
    } catch (e) {
      console.error('Failed to clear corrupted data:', e);
    }
    return null;
  }
}

// Auto-save with debouncing
function autoSaveChats() {
  if (saveDebounceTimer) {
    clearTimeout(saveDebounceTimer);
  }
  saveDebounceTimer = setTimeout(() => {
    saveChatsToStorage();
  }, SAVE_DEBOUNCE_MS);
}

// Function to check if a chat has an active request (for UI indicators)
function isChatRequestActive(chatId) {
  if (!chatId || !activeRequests) {
    return false;
  }
  const request = activeRequests.get(chatId);
  return request && (request.controller || request.reader);
}

// Helper function to update reactive tracking for UI indicators
function updateRequestTracking(chatId, isActive) {
  const chatStore = Alpine.store("chat");
  if (chatStore && typeof chatStore.updateActiveRequestTracking === 'function') {
    chatStore.updateActiveRequestTracking(chatId, isActive);
  }
}

// Make functions available globally
window.autoSaveChats = autoSaveChats;
window.createNewChat = createNewChat;
window.switchChat = switchChat;
window.deleteChat = deleteChat;
window.bulkDeleteChats = bulkDeleteChats;
window.updateChatName = updateChatName;
window.updateUIForActiveChat = updateUIForActiveChat;
window.isChatRequestActive = isChatRequestActive;

// Create a new chat
function createNewChat(model, systemPrompt, mcpMode) {
  if (!window.Alpine || !Alpine.store("chat")) {
    return null;
  }
  
  const chatStore = Alpine.store("chat");
  const chat = chatStore.createChat(model, systemPrompt, mcpMode);
  
  // Save to storage
  saveChatsToStorage();
  
  // Update UI to reflect new active chat
  updateUIForActiveChat();
  
  return chat;
}

// Switch to a different chat
function switchChat(chatId) {
  if (!window.Alpine || !Alpine.store("chat")) {
    return false;
  }
  
  const chatStore = Alpine.store("chat");
  const oldActiveChat = chatStore.activeChat();
  
  if (chatStore.switchChat(chatId)) {
    // CRITICAL: Stop interval FIRST before any other operations
    // This prevents the interval from updating with wrong chat's data
    if (tokensPerSecondInterval) {
      clearInterval(tokensPerSecondInterval);
      tokensPerSecondInterval = null;
    }
    
    // Immediately clear the display to prevent showing stale data
    const tokensPerSecondDisplay = document.getElementById('tokens-per-second');
    if (tokensPerSecondDisplay) {
      tokensPerSecondDisplay.textContent = '-';
    }
    
    // Save current state before switching
    saveChatsToStorage();
    
    // Hide badge when switching chats - will be shown if new chat has completed request
    const maxBadge = document.getElementById('max-tokens-per-second-badge');
    if (maxBadge) {
      maxBadge.style.display = 'none';
    }
    
    // Update global request tracking for stop button (only if new chat has active request)
    const newActiveChat = chatStore.activeChat();
    const newRequest = activeRequests.get(newActiveChat?.id);
    if (newRequest) {
      currentAbortController = newRequest.controller;
      currentReader = newRequest.reader;
      // Update loader state if new chat has active request
      const hasActiveRequest = newRequest.controller || newRequest.reader;
      if (hasActiveRequest) {
        toggleLoader(true, newActiveChat.id);
        // Wait a bit to ensure switch is complete and interval is stopped
        setTimeout(() => {
          // Double-check we're still on the same chat and interval is stopped
          const currentActiveChat = chatStore.activeChat();
          if (currentActiveChat && currentActiveChat.id === newActiveChat.id) {
            // Make absolutely sure interval is stopped
            if (tokensPerSecondInterval) {
              clearInterval(tokensPerSecondInterval);
              tokensPerSecondInterval = null;
              tokensPerSecondIntervalChatId = null;
            }
            // Update display for the new active chat
            updateTokensPerSecond(newActiveChat.id);
            // Restart interval to pick up the new active chat
            startTokensPerSecondInterval();
          }
        }, 100);
      } else {
        toggleLoader(false, newActiveChat.id);
      }
    } else {
      // No active request for new chat, clear global references
      currentAbortController = null;
      currentReader = null;
      toggleLoader(false, newActiveChat?.id);
      // Display is already cleared above
      
      // Check if this chat has a completed request with max tokens/s to show
      // Note: We only show badge for completed requests, not active ones
      // The badge will be shown when the request ends, not when switching to a chat
    }
    
    // Update UI to reflect new active chat
    updateUIForActiveChat();
    
    return true;
  }
  return false;
}

// Delete a chat
function deleteChat(chatId) {
  if (!window.Alpine || !Alpine.store("chat")) {
    return false;
  }
  
  const chatStore = Alpine.store("chat");
  
  // Prevent deleting the last chat
  if (chatStore.chats.length <= 1) {
    alert('Cannot delete the last chat. Please create a new chat first.');
    return false;
  }
  
  if (chatStore.deleteChat(chatId)) {
    // Ensure at least one chat exists after deletion
    if (chatStore.chats.length === 0) {
      const currentModel = document.getElementById("chat-model")?.value || "";
      chatStore.createChat(currentModel, "", false);
    }
    
    saveChatsToStorage();
    updateUIForActiveChat();
    return true;
  }
  return false;
}

// Bulk delete chats
function bulkDeleteChats(options) {
  if (!window.Alpine || !Alpine.store("chat")) {
    return 0;
  }
  
  const chatStore = Alpine.store("chat");
  let deletedCount = 0;
  const now = Date.now();
  
  if (options.deleteAll) {
    // Delete all chats except active one, or create new if deleting all
    const activeId = chatStore.activeChatId;
    chatStore.chats = chatStore.chats.filter(chat => {
      if (chat.id === activeId && chatStore.chats.length > 1) {
        return true; // Keep active chat if there are others
      }
      deletedCount++;
      return false;
    });
    
    // If all deleted, create a new chat
    if (chatStore.chats.length === 0) {
      chatStore.createChat();
    } else if (!chatStore.chats.find(c => c.id === activeId)) {
              // Active chat was deleted, switch to first available
              if (chatStore.chats.length > 0) {
                chatStore.activeChatId = chatStore.chats[0].id;
              }
            }
          } else if (options.olderThanDays) {
    const cutoffTime = now - (options.olderThanDays * 24 * 60 * 60 * 1000);
    const activeId = chatStore.activeChatId;
    
    chatStore.chats = chatStore.chats.filter(chat => {
      if (chat.id === activeId) {
        return true; // Never delete active chat
      }
      if (chat.updatedAt < cutoffTime) {
        deletedCount++;
        return false;
      }
      return true;
    });
    
    // Ensure at least one chat exists
    if (chatStore.chats.length === 0) {
      const currentModel = document.getElementById("chat-model")?.value || "";
      chatStore.createChat(currentModel, "", false);
    }
  }
  
  if (deletedCount > 0) {
    saveChatsToStorage();
    updateUIForActiveChat();
  }
  
  return deletedCount;
}

// Update UI elements to reflect active chat
function updateUIForActiveChat() {
  if (!window.Alpine || !Alpine.store("chat")) {
    return;
  }
  
  const chatStore = Alpine.store("chat");
  
  // Ensure at least one chat exists
  if (!chatStore.chats || chatStore.chats.length === 0) {
    const currentModel = document.getElementById("chat-model")?.value || "";
    chatStore.createChat(currentModel, "", false);
  }
  
  const activeChat = chatStore.activeChat();
  
  if (!activeChat) {
    // No active chat, set first one as active
    if (chatStore.chats.length > 0) {
      chatStore.activeChatId = chatStore.chats[0].id;
    } else {
      // Still no chats, create one
      const currentModel = document.getElementById("chat-model")?.value || "";
      chatStore.createChat(currentModel, "", false);
    }
    return;
  }
  
  // Update system prompt input
  const systemPromptInput = document.getElementById("systemPrompt");
  if (systemPromptInput) {
    systemPromptInput.value = activeChat.systemPrompt || "";
  }
  
  // Update MCP toggle
  const mcpToggle = document.getElementById("mcp-toggle");
  if (mcpToggle) {
    mcpToggle.checked = activeChat.mcpMode || false;
  }
  
  // Update model selector (if needed)
  const modelSelector = document.getElementById("modelSelector");
  if (modelSelector && activeChat.model) {
    // Find and select the option matching the active chat's model
    for (let option of modelSelector.options) {
      if (option.value === `chat/${activeChat.model}` || option.text === activeChat.model) {
        option.selected = true;
        break;
      }
    }
  }
  
  // Update chat model hidden input
  const chatModelInput = document.getElementById("chat-model");
  if (chatModelInput) {
    chatModelInput.value = activeChat.model || "";
  }
}

// Update chat name
function updateChatName(chatId, name) {
  if (!window.Alpine || !Alpine.store("chat")) {
    return false;
  }
  
  const chatStore = Alpine.store("chat");
  if (chatStore.updateChatName(chatId, name)) {
    autoSaveChats();
    return true;
  }
  return false;
}

function toggleLoader(show, chatId = null) {
  const sendButton = document.getElementById('send-button');
  const stopButton = document.getElementById('stop-button');
  const headerLoadingIndicator = document.getElementById('header-loading-indicator');
  const tokensPerSecondDisplay = document.getElementById('tokens-per-second');
  
  if (show) {
    sendButton.style.display = 'none';
    stopButton.style.display = 'block';
    if (headerLoadingIndicator) headerLoadingIndicator.style.display = 'block';
    
    // Start updating tokens/second display only if this is for the active chat
    const chatStore = Alpine.store("chat");
    const activeChat = chatStore.activeChat();
    
    // Always stop any existing interval first
    if (tokensPerSecondInterval) {
      clearInterval(tokensPerSecondInterval);
      tokensPerSecondInterval = null;
    }
    
    // Use provided chatId or get from active chat
    const targetChatId = chatId || (activeChat ? activeChat.id : null);
    
    if (tokensPerSecondDisplay && targetChatId && activeChat && activeChat.id === targetChatId) {
      tokensPerSecondDisplay.textContent = '-';
      // Hide max badge when starting new request
      const maxBadge = document.getElementById('max-tokens-per-second-badge');
      if (maxBadge) {
        maxBadge.style.display = 'none';
      }
      // Don't start interval here - it will be started when the request is created
      // Just update once to show initial state
      updateTokensPerSecond(targetChatId);
    } else if (tokensPerSecondDisplay) {
      // Not the active chat, hide or show dash
      tokensPerSecondDisplay.textContent = '-';
    }
  } else {
    sendButton.style.display = 'block';
    stopButton.style.display = 'none';
    if (headerLoadingIndicator) headerLoadingIndicator.style.display = 'none';
    // Stop updating but keep the last value visible only if this was the active chat
    const chatStore = Alpine.store("chat");
    const activeChat = chatStore.activeChat();
    if (chatId && activeChat && activeChat.id === chatId) {
      // Stop the interval since this request is done
      stopTokensPerSecondInterval();
      // Keep the last calculated rate visible
      if (tokensPerSecondDisplay && lastTokensPerSecond !== null) {
        tokensPerSecondDisplay.textContent = lastTokensPerSecond;
      }
      // Check if there are other active requests for the active chat and restart interval if needed
      const activeRequest = activeRequests.get(activeChat.id);
      if (activeRequest && (activeRequest.controller || activeRequest.reader)) {
        // Restart interval for the active chat
        startTokensPerSecondInterval();
      }
    } else if (tokensPerSecondDisplay) {
      // Not the active chat, just show dash
      tokensPerSecondDisplay.textContent = '-';
    }
    // Only clear global references if this was the active chat
    if (chatId && activeChat && activeChat.id === chatId) {
      currentAbortController = null;
      currentReader = null;
      
      // Show the max tokens/s badge when request ends
      const request = activeRequests.get(chatId);
      if (request && request.maxTokensPerSecond > 0) {
        updateMaxTokensPerSecondBadge(chatId, request.maxTokensPerSecond);
      }
    }
  }
}

// Start a single global interval that updates tokens/second for the active chat
function startTokensPerSecondInterval() {
  // Stop any existing interval first
  stopTokensPerSecondInterval();
  
  // Get the current active chat ID to track
  const chatStore = Alpine.store("chat");
  if (!chatStore) {
    return;
  }
  
  const activeChat = chatStore.activeChat();
  if (!activeChat) {
    return;
  }
  
  // Check if active chat has an active request
  // We can start the interval if we have at least a controller (reader will be set when streaming starts)
  const request = activeRequests.get(activeChat.id);
  if (!request) {
    // No active request for this chat
    return;
  }
  
  if (!request.controller) {
    // No controller yet, don't start interval
    return;
  }
  
  // Store which chat this interval is for
  tokensPerSecondIntervalChatId = activeChat.id;
  
  // Start a single interval that always checks the current active chat
  // Use a function that always gets fresh state, no closures
  tokensPerSecondInterval = setInterval(() => {
    // Always get fresh references - no closures
    const currentChatStore = Alpine.store("chat");
    if (!currentChatStore) {
      stopTokensPerSecondInterval();
      return;
    }
    
    const currentActiveChat = currentChatStore.activeChat();
    const tokensPerSecondDisplay = document.getElementById('tokens-per-second');
    
    if (!tokensPerSecondDisplay) {
      stopTokensPerSecondInterval();
      return;
    }
    
    // CRITICAL: Check if the active chat has changed
    if (!currentActiveChat || currentActiveChat.id !== tokensPerSecondIntervalChatId) {
      // Active chat changed, stop this interval immediately and hide badge
      const maxBadge = document.getElementById('max-tokens-per-second-badge');
      if (maxBadge) {
        maxBadge.style.display = 'none';
      }
      stopTokensPerSecondInterval();
      return;
    }
    
    // Check if active chat still has an active request
    const currentRequest = activeRequests.get(currentActiveChat.id);
    if (!currentRequest) {
      // No active request for this chat anymore - hide badge
      tokensPerSecondDisplay.textContent = '-';
      const maxBadge = document.getElementById('max-tokens-per-second-badge');
      if (maxBadge) {
        maxBadge.style.display = 'none';
      }
      stopTokensPerSecondInterval();
      return;
    }
    
    // If controller is gone, request ended - show max rate badge only for this chat
    if (!currentRequest.controller) {
      tokensPerSecondDisplay.textContent = '-';
      if (currentRequest.maxTokensPerSecond > 0) {
        // Only show badge if this is still the active chat
        updateMaxTokensPerSecondBadge(currentActiveChat.id, currentRequest.maxTokensPerSecond);
      } else {
        // Hide badge if no max value
        const maxBadge = document.getElementById('max-tokens-per-second-badge');
        if (maxBadge) {
          maxBadge.style.display = 'none';
        }
      }
      stopTokensPerSecondInterval();
      return;
    }
    
    // Update for the current active chat only
    updateTokensPerSecond(currentActiveChat.id);
  }, 250); // Update more frequently for better responsiveness
}

// Stop the tokens/second interval
function stopTokensPerSecondInterval() {
  if (tokensPerSecondInterval) {
    clearInterval(tokensPerSecondInterval);
    tokensPerSecondInterval = null;
  }
  tokensPerSecondIntervalChatId = null; // Clear tracked chat ID
  const tokensPerSecondDisplay = document.getElementById('tokens-per-second');
  if (tokensPerSecondDisplay) {
    tokensPerSecondDisplay.textContent = '-';
  }
  // Clear the last rate so it doesn't get reused
  lastTokensPerSecond = null;
}

function updateTokensPerSecond(chatId) {
  const tokensPerSecondDisplay = document.getElementById('tokens-per-second');
  if (!tokensPerSecondDisplay || !chatId) {
    return;
  }
  
  // Get the request info for this chat
  const request = activeRequests.get(chatId);
  if (!request || !request.startTime) {
    tokensPerSecondDisplay.textContent = '-';
    return;
  }
  
  // Verify the request is still active (controller is cleared when request ends)
  if (!request.controller) {
    tokensPerSecondDisplay.textContent = '-';
    return;
  }
  
  // Check if this is still the active chat
  const chatStore = Alpine.store("chat");
  const activeChat = chatStore ? chatStore.activeChat() : null;
  if (!activeChat || activeChat.id !== chatId) {
    // Not the active chat anymore
    tokensPerSecondDisplay.textContent = '-';
    return;
  }
  
  const elapsedSeconds = (Date.now() - request.startTime) / 1000;
  // Show rate if we have tokens, otherwise show waiting indicator
  if (elapsedSeconds > 0) {
    if (request.tokensReceived > 0) {
      const rate = request.tokensReceived / elapsedSeconds;
      // Update max rate if this is higher
      if (rate > (request.maxTokensPerSecond || 0)) {
        request.maxTokensPerSecond = rate;
      }
      const formattedRate = `${rate.toFixed(1)} tokens/s`;
      tokensPerSecondDisplay.textContent = formattedRate;
      lastTokensPerSecond = formattedRate; // Store the last calculated rate
      
      // Update the max badge if it exists (only show during active request if user wants, or we can show it at the end)
    } else {
      // Request is active but no tokens yet - show waiting
      tokensPerSecondDisplay.textContent = '0.0 tokens/s';
    }
  } else {
    // Just started
    tokensPerSecondDisplay.textContent = '-';
  }
}

// Update the max tokens/s badge display
function updateMaxTokensPerSecondBadge(chatId, maxRate) {
  const maxBadge = document.getElementById('max-tokens-per-second-badge');
  if (!maxBadge) return;
  
  // Check if this is still the active chat
  const chatStore = Alpine.store("chat");
  const activeChat = chatStore ? chatStore.activeChat() : null;
  if (!activeChat || activeChat.id !== chatId) {
    // Not the active chat, hide badge
    maxBadge.style.display = 'none';
    return;
  }
  
  // Only show badge if we have a valid max rate
  if (maxRate > 0) {
    maxBadge.textContent = `Peak: ${maxRate.toFixed(1)} tokens/s`;
    maxBadge.style.display = 'inline-flex';
  } else {
    maxBadge.style.display = 'none';
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
  // Stop the request for the currently active chat
  const chatStore = Alpine.store("chat");
  const activeChat = chatStore.activeChat();
  if (!activeChat) return;
  
  const request = activeRequests.get(activeChat.id);
  if (request) {
    if (request.controller) {
      request.controller.abort();
    }
    if (request.reader) {
      request.reader.cancel();
    }
    if (request.interval) {
      clearInterval(request.interval);
    }
    activeRequests.delete(activeChat.id);
    updateRequestTracking(activeChat.id, false);
  }
  
  // Also clear global references
  if (currentAbortController) {
    currentAbortController.abort();
    currentAbortController = null;
  }
  if (currentReader) {
    currentReader.cancel();
    currentReader = null;
  }
  toggleLoader(false, activeChat.id);
  chatStore.add(
    "assistant",
    `<span class='error'>Request cancelled by user</span>`,
    null,
    null,
    activeChat.id
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
  const chatStore = Alpine.store("chat");
  const activeChat = chatStore.activeChat();
  if (activeChat) {
    activeChat.systemPrompt = document.getElementById("systemPrompt").value;
    activeChat.updatedAt = Date.now();
    autoSaveChats();
  }
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

  // Check if there's an active request for the current chat
  const chatStore = Alpine.store("chat");
  const activeChat = chatStore.activeChat();
  if (activeChat) {
    const activeRequest = activeRequests.get(activeChat.id);
    if (activeRequest && (activeRequest.controller || activeRequest.reader)) {
      // Abort current request for this chat
      stopRequest();
      // Small delay to ensure cleanup completes
      setTimeout(() => {
        // Continue with new request
        processAndSendMessage(inputValue);
      }, 100);
      return;
    }
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
  
  // Add the message to the chat UI with just the icons
  Alpine.store("chat").add("user", displayContent, images, audios);
  
  // Update the last message in the store with the full content
  const chatStore = Alpine.store("chat");
  const activeChat = chatStore.activeChat();
  if (activeChat && activeChat.history.length > 0) {
    activeChat.history[activeChat.history.length - 1].content = fullInput;
    activeChat.updatedAt = Date.now();
  }
  
  const input = document.getElementById("input");
  if (input) input.value = "";
  const systemPrompt = activeChat?.systemPrompt || "";
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
  const chatStore = Alpine.store("chat");
  const activeChat = chatStore.activeChat();
  if (!activeChat) {
    console.error('No active chat');
    return;
  }
  
  const model = activeChat.model || document.getElementById("chat-model").value;
  const mcpMode = activeChat.mcpMode || false;
  
  // Reset current request usage tracking for new request
  if (activeChat.tokenUsage) {
    activeChat.tokenUsage.currentRequest = null;
  }
  
  // Store the chat ID for this request so we can track it even if user switches chats
  const chatId = activeChat.id;
  
  toggleLoader(true, chatId);

  messages = chatStore.messages();

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
  
  // Add generation parameters if they are set (null means use default)
  if (activeChat.temperature !== null && activeChat.temperature !== undefined) {
    requestBody.temperature = activeChat.temperature;
  }
  if (activeChat.topP !== null && activeChat.topP !== undefined) {
    requestBody.top_p = activeChat.topP;
  }
  if (activeChat.topK !== null && activeChat.topK !== undefined) {
    requestBody.top_k = activeChat.topK;
  }
  
  let response;
  try {
    // Create AbortController for timeout handling and stop button
    const controller = new AbortController();
    // Store per-chat so switching chats doesn't abort this request
    const requestStartTime = Date.now();
    activeRequests.set(chatId, {
      controller: controller,
      reader: null,
      startTime: requestStartTime,
      tokensReceived: 0,
      interval: null,
      maxTokensPerSecond: 0
    });
    
    // Update reactive tracking for UI indicators
    updateRequestTracking(chatId, true);
    // Also store globally for stop button (only for active chat)
    currentAbortController = controller;
    
    // Start tokens/second interval now that the request is created
    // Try to start immediately, and also schedule a retry in case Alpine isn't ready
    startTokensPerSecondInterval();
    setTimeout(() => {
      // Retry in case the first attempt failed due to timing
      if (!tokensPerSecondInterval) {
        startTokensPerSecondInterval();
      }
    }, 200);
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
        chatStore.add(
          "assistant",
          `<span class='error'>Request timeout: MCP processing is taking longer than expected. Please try again.</span>`,
          null,
          null,
          chatId
        );
      }
    } else {
      chatStore.add(
        "assistant",
        `<span class='error'>Network Error: ${error.message}</span>`,
        null,
        null,
        chatId
      );
    }
    toggleLoader(false, chatId);
    activeRequests.delete(chatId);
    updateRequestTracking(chatId, false);
    const activeChat = chatStore.activeChat();
    if (activeChat && activeChat.id === chatId) {
      currentAbortController = null;
    }
    return;
  }

  if (!response.ok) {
    chatStore.add(
      "assistant",
      `<span class='error'>Error: POST ${endpoint} ${response.status}</span>`,
      null,
      null,
      chatId
    );
    toggleLoader(false, chatId);
    activeRequests.delete(chatId);
    updateRequestTracking(chatId, false);
    const activeChat = chatStore.activeChat();
    if (activeChat && activeChat.id === chatId) {
      currentAbortController = null;
    }
    return;
  }

  // Handle streaming response (both regular and MCP mode now use SSE)
  if (mcpMode) {
    // Handle MCP SSE streaming with new event types
    const reader = response.body
      ?.pipeThrough(new TextDecoderStream())
      .getReader();

    if (!reader) {
      chatStore.add(
        "assistant",
        `<span class='error'>Error: Failed to decode MCP API response</span>`,
        null,
        null,
        chatId
      );
      toggleLoader(false, chatId);
      activeRequests.delete(chatId);
      return;
    }

    // Store reader per-chat and globally
    const mcpRequest = activeRequests.get(chatId);
    if (mcpRequest) {
      mcpRequest.reader = reader;
      // Ensure tracking is updated when reader is set
      updateRequestTracking(chatId, true);
    }
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

        // Check if chat still exists and is still the target chat (user might have switched)
        const currentChat = chatStore.getChat(chatId);
        if (!currentChat) {
          // Chat was deleted, abort
          break;
        }
        const targetHistory = currentChat.history;

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
                    // Insert reasoning before assistant message if it exists
                    if (lastAssistantMessageIndex >= 0 && targetHistory[lastAssistantMessageIndex]?.role === "assistant") {
                      targetHistory.splice(lastAssistantMessageIndex, 0, {
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
                      chatStore.add("reasoning", eventData.content, null, null, chatId);
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
                    chatStore.add("tool_call", JSON.stringify(toolCallData, null, 2), null, null, chatId);
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
                    chatStore.add("tool_result", JSON.stringify(toolResultData, null, 2), null, null, chatId);
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
                    
                    // Count tokens for rate calculation (per chat)
                    const request = activeRequests.get(chatId);
                    if (request) {
                      request.tokensReceived += Math.ceil(contentChunk.length / 4);
                    }
                    // Only update display if this is the active chat (interval will handle it)
                    // Don't call updateTokensPerSecond here to avoid unnecessary updates
                    
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
                          const currentChat = chatStore.getChat(chatId);
                          if (!currentChat) break; // Chat was deleted
                          const isMCPMode = currentChat.mcpMode || false;
                          const shouldExpand = !isMCPMode; // Expanded in non-MCP mode, collapsed in MCP mode
                          if (lastThinkingMessageIndex === -1) {
                            // Insert thinking before the last assistant message if it exists
                            if (lastAssistantMessageIndex >= 0 && targetHistory[lastAssistantMessageIndex]?.role === "assistant") {
                              // Insert before assistant message
                              targetHistory.splice(lastAssistantMessageIndex, 0, {
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
                              chatStore.add("thinking", extractedThinking, null, null, chatId);
                              lastThinkingMessageIndex = targetHistory.length - 1;
                            }
                          } else {
                            // Update existing thinking message
                            const lastMessage = targetHistory[lastThinkingMessageIndex];
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
                      const currentChat = chatStore.getChat(chatId);
                      if (!currentChat) break; // Chat was deleted
                      const isMCPMode = currentChat.mcpMode || false;
                      const shouldExpand = !isMCPMode; // Expanded in non-MCP mode, collapsed in MCP mode
                      // Update the last thinking message or create a new one (incremental)
                      if (lastThinkingMessageIndex === -1) {
                        // Insert thinking before the last assistant message if it exists
                        if (lastAssistantMessageIndex >= 0 && targetHistory[lastAssistantMessageIndex]?.role === "assistant") {
                          // Insert before assistant message
                          targetHistory.splice(lastAssistantMessageIndex, 0, {
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
                          chatStore.add("thinking", thinkingContent, null, null, chatId);
                          lastThinkingMessageIndex = targetHistory.length - 1;
                        }
                      } else {
                        // Update existing thinking message
                        const lastMessage = targetHistory[lastThinkingMessageIndex];
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
                  chatStore.add(
                    "assistant",
                    `<span class='error'>MCP Error: ${eventData.message}</span>`,
                    null,
                    null,
                    chatId
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
          const currentChat = chatStore.getChat(chatId);
          if (!currentChat) break; // Chat was deleted
          if (lastAssistantMessageIndex === -1) {
            if (processedRegular && processedRegular.trim()) {
              chatStore.add("assistant", processedRegular, null, null, chatId);
              lastAssistantMessageIndex = targetHistory.length - 1;
            }
          } else {
            const lastMessage = targetHistory[lastAssistantMessageIndex];
            if (lastMessage && lastMessage.role === "assistant") {
              lastMessage.content = (lastMessage.content || "") + (processedRegular || "");
              lastMessage.html = DOMPurify.sanitize(marked.parse(lastMessage.content));
            }
          }
          
          // Add any extracted thinking content from the processed buffer BEFORE assistant message
          if (processedThinking && processedThinking.trim()) {
            const isMCPMode = currentChat.mcpMode || false;
            const shouldExpand = !isMCPMode; // Expanded in non-MCP mode, collapsed in MCP mode
            // Insert thinking before assistant message if it exists
            if (lastAssistantMessageIndex >= 0 && targetHistory[lastAssistantMessageIndex]?.role === "assistant") {
              targetHistory.splice(lastAssistantMessageIndex, 0, {
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
              chatStore.add("thinking", processedThinking, null, null, chatId);
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
        
        const currentChat = chatStore.getChat(chatId);
        if (!currentChat) {
          // Chat was deleted, cleanup and exit
          activeRequests.delete(chatId);
          updateRequestTracking(chatId, false);
          return;
        }
        const targetHistory = currentChat.history;
        
        // First, add any extracted thinking content BEFORE assistant message
        if (processedThinking && processedThinking.trim()) {
          const isMCPMode = currentChat.mcpMode || false;
          const shouldExpand = !isMCPMode; // Expanded in non-MCP mode, collapsed in MCP mode
          // Insert thinking before assistant message if it exists
          if (lastAssistantMessageIndex >= 0 && targetHistory[lastAssistantMessageIndex]?.role === "assistant") {
            targetHistory.splice(lastAssistantMessageIndex, 0, {
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
            chatStore.add("thinking", processedThinking, null, null, chatId);
          }
        }
        
        // Then update or create assistant message
        if (lastAssistantMessageIndex !== -1) {
          const lastMessage = targetHistory[lastAssistantMessageIndex];
          if (lastMessage && lastMessage.role === "assistant") {
            lastMessage.content = (lastMessage.content || "") + (processedRegular || "");
            lastMessage.html = DOMPurify.sanitize(marked.parse(lastMessage.content));
          }
        } else if (processedRegular && processedRegular.trim()) {
          chatStore.add("assistant", processedRegular, null, null, chatId);
          lastAssistantMessageIndex = targetHistory.length - 1;
        }
      }
      
      // Final thinking content flush if any data remains (from incremental detection)
      const finalChat = chatStore.getChat(chatId);
      if (finalChat && thinkingContent.trim() && lastThinkingMessageIndex === -1) {
        const finalHistory = finalChat.history;
        // Extract thinking content if tags are present
        const thinkingMatch = thinkingContent.match(/<(?:thinking|redacted_reasoning)>(.*?)<\/(?:thinking|redacted_reasoning)>/s);
        if (thinkingMatch && thinkingMatch[1]) {
          const isMCPMode = finalChat.mcpMode || false;
          const shouldExpand = !isMCPMode; // Expanded in non-MCP mode, collapsed in MCP mode
          // Insert thinking before assistant message if it exists
          if (lastAssistantMessageIndex >= 0 && finalHistory[lastAssistantMessageIndex]?.role === "assistant") {
            finalHistory.splice(lastAssistantMessageIndex, 0, {
              role: "thinking",
              content: thinkingMatch[1],
              html: DOMPurify.sanitize(marked.parse(thinkingMatch[1])),
              image: [],
              audio: [],
              expanded: shouldExpand
            });
          } else {
            // No assistant message yet, just add normally
            chatStore.add("thinking", thinkingMatch[1], null, null, chatId);
          }
        } else {
          chatStore.add("thinking", thinkingContent, null, null, chatId);
        }
      }
      
      // Final pass: process the entire assistantContent to catch any missed thinking tags
      // This ensures we don't miss tags that were split across chunks
      if (finalChat && assistantContent.trim()) {
        const finalHistory = finalChat.history;
        const { regularContent: finalRegular, thinkingContent: finalThinking } = processThinkingTags(assistantContent);
        
        // Update assistant message with final processed content (without thinking tags)
        if (finalRegular && finalRegular.trim()) {
          if (lastAssistantMessageIndex !== -1) {
            const lastMessage = finalHistory[lastAssistantMessageIndex];
            if (lastMessage && lastMessage.role === "assistant") {
              lastMessage.content = finalRegular;
              lastMessage.html = DOMPurify.sanitize(marked.parse(lastMessage.content));
            }
          } else {
            chatStore.add("assistant", finalRegular, null, null, chatId);
          }
        }
        
        // Add any extracted thinking content (only if not already added)
        if (finalThinking && finalThinking.trim()) {
          const hasThinking = finalHistory.some(msg => 
            msg.role === "thinking" && msg.content.trim() === finalThinking.trim()
          );
          if (!hasThinking) {
            chatStore.add("thinking", finalThinking, null, null, chatId);
          }
        }
      }
      
      // Cleanup request tracking
      activeRequests.delete(chatId);
      updateRequestTracking(chatId, false);

      // Highlight all code blocks once at the end
      hljs.highlightAll();
    } catch (error) {
      // Don't show error if request was aborted by user
      if (error.name !== 'AbortError' || !currentAbortController) {
        const errorChat = chatStore.getChat(chatId);
        if (errorChat) {
          chatStore.add(
            "assistant",
            `<span class='error'>Error: Failed to process MCP stream</span>`,
            null,
            null,
            chatId
          );
        }
      }
    } finally {
      // Perform any cleanup if necessary
      if (reader) {
        reader.releaseLock();
      }
      // Only clear global references if this was the active chat's request
      const activeChat = chatStore.activeChat();
      if (activeChat && activeChat.id === chatId) {
        currentReader = null;
        currentAbortController = null;
        toggleLoader(false, chatId);
      }
      // Cleanup per-chat tracking
      activeRequests.delete(chatId);
      updateRequestTracking(chatId, false);
    }
  } else {
    // Handle regular streaming response
    const reader = response.body
      ?.pipeThrough(new TextDecoderStream())
      .getReader();

    if (!reader) {
      chatStore.add(
        "assistant",
        `<span class='error'>Error: Failed to decode API response</span>`,
        null,
        null,
        chatId
      );
      toggleLoader(false, chatId);
      activeRequests.delete(chatId);
      return;
    }

    // Store reader per-chat and globally
    const request = activeRequests.get(chatId);
    if (request) {
      request.reader = reader;
      // Ensure tracking is updated when reader is set
      updateRequestTracking(chatId, true);
      // Ensure interval is running (in case it wasn't started earlier)
      startTokensPerSecondInterval();
    }
    currentReader = reader;

    // Get target chat for this request
    let targetChat = chatStore.getChat(chatId);
    if (!targetChat) {
      // Chat was deleted
      activeRequests.delete(chatId);
      updateRequestTracking(chatId, false);
      return;
    }

    // Function to add content to the chat and handle DOM updates efficiently
    const addToChat = (token) => {
      const currentChat = chatStore.getChat(chatId);
      if (!currentChat) return; // Chat was deleted
      chatStore.add("assistant", token, null, null, chatId);
      // Count tokens for rate calculation (per chat)
      const request = activeRequests.get(chatId);
      if (request) {
        const tokenCount = Math.ceil(token.length / 4);
        request.tokensReceived += tokenCount;
      }
      // Only update display if this is the active chat (interval will handle it)
      // Don't call updateTokensPerSecond here to avoid unnecessary updates
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

        // Check if chat still exists
        targetChat = chatStore.getChat(chatId);
        if (!targetChat) {
          // Chat was deleted, abort
          break;
        }
        const targetHistory = targetChat.history;

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
              
              // Update token usage if present (for the chat that initiated this request)
              if (jsonData.usage) {
                chatStore.updateTokenUsage(jsonData.usage, chatId);
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
                      chatStore.add("thinking", thinkingContent, null, null, chatId);
                    }
                  }
                  return;
                }

                // Handle content based on thinking state
                if (isThinking) {
                  thinkingContent += token;
                  // Count tokens for rate calculation (per chat)
                  const request = activeRequests.get(chatId);
                  if (request) {
                    request.tokensReceived += Math.ceil(token.length / 4);
                  }
                  // Only update display if this is the active chat (interval will handle it)
                  // Don't call updateTokensPerSecond here to avoid unnecessary updates
                  // Update the last thinking message or create a new one
                  if (lastThinkingMessageIndex === -1) {
                    // Create new thinking message
                    chatStore.add("thinking", thinkingContent, null, null, chatId);
                    const targetChat = chatStore.getChat(chatId);
                    lastThinkingMessageIndex = targetChat ? targetChat.history.length - 1 : -1;
                  } else {
                    // Update existing thinking message
                    const currentChat = chatStore.getChat(chatId);
                    if (currentChat && lastThinkingMessageIndex >= 0) {
                      const lastMessage = currentChat.history[lastThinkingMessageIndex];
                      if (lastMessage && lastMessage.role === "thinking") {
                        lastMessage.content = thinkingContent;
                        lastMessage.html = DOMPurify.sanitize(marked.parse(thinkingContent));
                      }
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
      const finalChat = chatStore.getChat(chatId);
      if (finalChat && thinkingContent.trim() && lastThinkingMessageIndex === -1) {
        chatStore.add("thinking", thinkingContent, null, null, chatId);
      }

      // Highlight all code blocks once at the end
      hljs.highlightAll();
    } catch (error) {
      // Don't show error if request was aborted by user
      if (error.name !== 'AbortError' || !currentAbortController) {
        const currentChat = chatStore.getChat(chatId);
        if (currentChat) {
          chatStore.add(
            "assistant",
            `<span class='error'>Error: Failed to process stream</span>`,
            null,
            null,
            chatId
          );
        }
      }
    } finally {
      // Perform any cleanup if necessary
      if (reader) {
        reader.releaseLock();
      }
      // Only clear global references if this was the active chat's request
      const activeChat = chatStore.activeChat();
      if (activeChat && activeChat.id === chatId) {
        currentReader = null;
        currentAbortController = null;
        toggleLoader(false, chatId);
      }
      // Cleanup per-chat tracking
      activeRequests.delete(chatId);
      updateRequestTracking(chatId, false);
    }
  }

  // Remove class "loader" from the element with "loader" id
  // Only toggle loader off if this was the active chat
  const finalActiveChat = chatStore.activeChat();
  if (finalActiveChat && finalActiveChat.id === chatId) {
    toggleLoader(false, chatId);
  }

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

document.getElementById("system_prompt").addEventListener("submit", submitSystemPrompt);
document.getElementById("prompt").addEventListener("submit", submitPrompt);
document.getElementById("input").focus();

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

// Alpine store is now initialized in chat.html inline script to ensure it's available before Alpine processes the DOM
// Only initialize if not already initialized (to avoid duplicate initialization)
document.addEventListener("alpine:init", () => {
  // Check if store already exists (initialized in chat.html)
  if (!Alpine.store("chat")) {
    // Fallback initialization (should not be needed if chat.html loads correctly)
    // This matches the structure in chat.html
    function generateChatId() {
      return "chat_" + Date.now() + "_" + Math.random().toString(36).substr(2, 9);
    }
    
    function getCurrentModel() {
      const modelInput = document.getElementById("chat-model");
      return modelInput ? modelInput.value : "";
    }
    
    Alpine.store("chat", {
      chats: [],
      activeChatId: null,
      chatIdCounter: 0,
      languages: [undefined],
      activeRequestIds: [], // Track chat IDs with active requests for UI reactivity
      
      activeChat() {
        if (!this.activeChatId) return null;
        return this.chats.find(c => c.id === this.activeChatId) || null;
      },
      
      getChat(chatId) {
        return this.chats.find(c => c.id === chatId) || null;
      },
      
      createChat(model, systemPrompt, mcpMode) {
        const chatId = generateChatId();
        const now = Date.now();
        const chat = {
          id: chatId,
          name: "New Chat",
          model: model || getCurrentModel() || "",
          history: [],
          systemPrompt: systemPrompt || "",
          mcpMode: mcpMode || false,
          tokenUsage: {
            promptTokens: 0,
            completionTokens: 0,
            totalTokens: 0,
            currentRequest: null
          },
          contextSize: null,
          createdAt: now,
          updatedAt: now
        };
        this.chats.push(chat);
        this.activeChatId = chatId;
        return chat;
      },
      
      switchChat(chatId) {
        if (this.chats.find(c => c.id === chatId)) {
          this.activeChatId = chatId;
          return true;
        }
        return false;
      },
      
      deleteChat(chatId) {
        const index = this.chats.findIndex(c => c.id === chatId);
        if (index === -1) return false;
        
        this.chats.splice(index, 1);
        
        if (this.activeChatId === chatId) {
          if (this.chats.length > 0) {
            this.activeChatId = this.chats[0].id;
          } else {
            this.createChat();
          }
        }
        return true;
      },
      
      updateChatName(chatId, name) {
        const chat = this.getChat(chatId);
        if (chat) {
          chat.name = name || "New Chat";
          chat.updatedAt = Date.now();
          return true;
        }
        return false;
      },
      
      clear() {
        const chat = this.activeChat();
        if (chat) {
          chat.history.length = 0;
          chat.tokenUsage = {
            promptTokens: 0,
            completionTokens: 0,
            totalTokens: 0,
            currentRequest: null
          };
          chat.updatedAt = Date.now();
        }
      },
      
      updateTokenUsage(usage, targetChatId = null) {
        // If targetChatId is provided, update that chat, otherwise use active chat
        // This ensures token usage updates go to the chat that initiated the request
        const chat = targetChatId ? this.getChat(targetChatId) : this.activeChat();
        if (!chat) return;
        
        if (usage) {
          const currentRequest = chat.tokenUsage.currentRequest || {
            promptTokens: 0,
            completionTokens: 0,
            totalTokens: 0
          };
          
          const isNewUsage = 
            (usage.prompt_tokens !== undefined && usage.prompt_tokens > currentRequest.promptTokens) ||
            (usage.completion_tokens !== undefined && usage.completion_tokens > currentRequest.completionTokens) ||
            (usage.total_tokens !== undefined && usage.total_tokens > currentRequest.totalTokens);
          
          if (isNewUsage) {
            chat.tokenUsage.promptTokens = chat.tokenUsage.promptTokens - currentRequest.promptTokens + (usage.prompt_tokens || 0);
            chat.tokenUsage.completionTokens = chat.tokenUsage.completionTokens - currentRequest.completionTokens + (usage.completion_tokens || 0);
            chat.tokenUsage.totalTokens = chat.tokenUsage.totalTokens - currentRequest.totalTokens + (usage.total_tokens || 0);
            
            chat.tokenUsage.currentRequest = {
              promptTokens: usage.prompt_tokens || 0,
              completionTokens: usage.completion_tokens || 0,
              totalTokens: usage.total_tokens || 0
            };
            chat.updatedAt = Date.now();
          }
        }
      },
      
      getRemainingTokens() {
        const chat = this.activeChat();
        if (!chat || !chat.contextSize) return null;
        return Math.max(0, chat.contextSize - chat.tokenUsage.totalTokens);
      },
      
      getContextUsagePercent() {
        const chat = this.activeChat();
        if (!chat || !chat.contextSize) return null;
        return Math.min(100, (chat.tokenUsage.totalTokens / chat.contextSize) * 100);
      },
      
      // Check if a chat has an active request (for UI indicators)
      hasActiveRequest(chatId) {
        if (!chatId) return false;
        // Use reactive array for Alpine.js reactivity
        return this.activeRequestIds.includes(chatId);
      },
      
      // Update active request tracking (called from chat.js)
      updateActiveRequestTracking(chatId, isActive) {
        if (isActive) {
          if (!this.activeRequestIds.includes(chatId)) {
            this.activeRequestIds.push(chatId);
          }
        } else {
          const index = this.activeRequestIds.indexOf(chatId);
          if (index > -1) {
            this.activeRequestIds.splice(index, 1);
          }
        }
      },
      
      add(role, content, image, audio, targetChatId = null) {
        // If targetChatId is provided, add to that chat, otherwise use active chat
        const chat = targetChatId ? this.getChat(targetChatId) : this.activeChat();
        if (!chat) return;
        
        const N = chat.history.length - 1;
        if (role === "thinking" || role === "reasoning") {
          let c = "";
          const lines = content.split("\n");
          lines.forEach((line) => {
            c += DOMPurify.sanitize(marked.parse(line));
          });
          chat.history.push({ role, content, html: c, image, audio });
        }
        else if (chat.history.length && chat.history[N].role === role) {
          chat.history[N].content += content;
          chat.history[N].html = DOMPurify.sanitize(
            marked.parse(chat.history[N].content)
          );
          if (image && image.length > 0) {
            chat.history[N].image = [...(chat.history[N].image || []), ...image];
          }
          if (audio && audio.length > 0) {
            chat.history[N].audio = [...(chat.history[N].audio || []), ...audio];
          }
        } else {
          let c = "";
          const lines = content.split("\n");
          lines.forEach((line) => {
            c += DOMPurify.sanitize(marked.parse(line));
          });
          chat.history.push({ 
            role, 
            content, 
            html: c, 
            image: image || [], 
            audio: audio || [] 
          });
          
          if (role === "user" && chat.name === "New Chat" && content.trim()) {
            const name = content.trim().substring(0, 50);
            chat.name = name.length < content.trim().length ? name + "..." : name;
          }
        }
        
        chat.updatedAt = Date.now();
        
        const chatContainer = document.getElementById('chat');
        if (chatContainer) {
          chatContainer.scrollTo({
            top: chatContainer.scrollHeight,
            behavior: 'smooth'
          });
        }
        if (role === "thinking" || role === "reasoning") {
          setTimeout(() => {
            if (typeof window.scrollThinkingBoxToBottom === 'function') {
              window.scrollThinkingBoxToBottom();
            }
          }, 100);
        }
        const parser = new DOMParser();
        const html = parser.parseFromString(
          chat.history[chat.history.length - 1].html,
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
        const chat = this.activeChat();
        if (!chat) return [];
        return chat.history.map((message) => ({
          role: message.role,
          content: message.content,
          image: message.image,
          audio: message.audio,
        }));
      },
      
      // Getter for active chat history to ensure reactivity
      get activeHistory() {
        const chat = this.activeChat();
        return chat ? chat.history : [];
      },
    });
  }
});

// Check for message from index page on load and initialize chats
document.addEventListener('DOMContentLoaded', function() {
  // Wait for Alpine to be ready
  setTimeout(() => {
    if (!window.Alpine || !Alpine.store("chat")) {
      console.error('Alpine store not initialized');
      return;
    }
    
    const chatStore = Alpine.store("chat");
    
    // Check for message from index page FIRST - if present, create new chat
    const chatData = localStorage.getItem('localai_index_chat_data');
    let shouldCreateNewChat = false;
    let indexChatData = null;
    
    if (chatData) {
      try {
        indexChatData = JSON.parse(chatData);
        shouldCreateNewChat = true; // We have data from index, create new chat
      } catch (error) {
        console.error('Error parsing chat data from index:', error);
        localStorage.removeItem('localai_index_chat_data');
      }
    }
    
    // Load chats from storage FIRST (but don't set active yet if we're creating new from index)
    const storedData = loadChatsFromStorage();
    
    if (storedData && storedData.chats && storedData.chats.length > 0) {
      // Restore chats from storage - clear existing and push new ones to maintain reactivity
      chatStore.chats.length = 0;
      storedData.chats.forEach(chat => {
        chatStore.chats.push(chat);
      });
      // Don't set activeChatId yet if we're creating a new chat from index
      if (!shouldCreateNewChat) {
        chatStore.activeChatId = storedData.activeChatId || storedData.chats[0].id;
        
        // Ensure active chat exists
        if (!chatStore.activeChat()) {
          chatStore.activeChatId = storedData.chats[0].id;
        }
      }
    }
    
    if (shouldCreateNewChat) {
      // Create a new chat with the model from URL (which matches the selected model from index)
      const currentModel = document.getElementById("chat-model")?.value || "";
      // Check URL parameter for MCP mode (takes precedence over localStorage)
      const urlParams = new URLSearchParams(window.location.search);
      const mcpFromUrl = urlParams.get('mcp') === 'true';
      const newChat = chatStore.createChat(currentModel, "", mcpFromUrl || indexChatData.mcpMode || false);
      
      // Update context size from template if available
      const contextSizeInput = document.getElementById("chat-model");
      if (contextSizeInput && contextSizeInput.dataset.contextSize) {
        const contextSize = parseInt(contextSizeInput.dataset.contextSize);
        newChat.contextSize = contextSize;
      }
      
      // Set the message and files
      const input = document.getElementById('input');
      if (input && indexChatData.message) {
        input.value = indexChatData.message;
        
        // Process files if any
        if (indexChatData.imageFiles && indexChatData.imageFiles.length > 0) {
          indexChatData.imageFiles.forEach(file => {
            images.push(file.data);
          });
        }
        
        if (indexChatData.audioFiles && indexChatData.audioFiles.length > 0) {
          indexChatData.audioFiles.forEach(file => {
            audios.push(file.data);
          });
        }
        
        if (indexChatData.textFiles && indexChatData.textFiles.length > 0) {
          indexChatData.textFiles.forEach(file => {
            fileContents.push({ name: file.name, content: file.data });
            currentFileNames.push(file.name);
          });
        }
        
        // Clear localStorage
        localStorage.removeItem('localai_index_chat_data');
        
        // Save the new chat
        saveChatsToStorage();
        
        // Update UI to reflect new active chat
        updateUIForActiveChat();
        
        // Auto-submit after a short delay to ensure everything is ready
        setTimeout(() => {
          if (input.value.trim()) {
            processAndSendMessage(input.value);
          }
        }, 500);
      } else {
        // No message, but might have mcpMode from URL - clear localStorage
        localStorage.removeItem('localai_index_chat_data');
        
        // If MCP mode was set from URL, ensure it's enabled
        const urlParams = new URLSearchParams(window.location.search);
        if (urlParams.get('mcp') === 'true' && newChat) {
          newChat.mcpMode = true;
          saveChatsToStorage();
          updateUIForActiveChat();
        }
        saveChatsToStorage();
        updateUIForActiveChat();
      }
    } else {
      // Normal flow: create default chat if none exist
      if (!storedData || !storedData.chats || storedData.chats.length === 0) {
        const currentModel = document.getElementById("chat-model")?.value || "";
        const oldSystemPrompt = localStorage.getItem(SYSTEM_PROMPT_STORAGE_KEY);
        // Check URL parameter for MCP mode
        const urlParams = new URLSearchParams(window.location.search);
        const mcpFromUrl = urlParams.get('mcp') === 'true';
        chatStore.createChat(currentModel, oldSystemPrompt || "", mcpFromUrl);
        
        // Remove old system prompt key after migration
        if (oldSystemPrompt) {
          localStorage.removeItem(SYSTEM_PROMPT_STORAGE_KEY);
        }
      } else {
        // Existing chats loaded - check URL parameter for MCP mode
        const urlParams = new URLSearchParams(window.location.search);
        if (urlParams.get('mcp') === 'true') {
          const activeChat = chatStore.activeChat();
          if (activeChat) {
            activeChat.mcpMode = true;
            saveChatsToStorage();
          }
        }
      }
      
      // Update context size from template if available
      const contextSizeInput = document.getElementById("chat-model");
      if (contextSizeInput && contextSizeInput.dataset.contextSize) {
        const contextSize = parseInt(contextSizeInput.dataset.contextSize);
        const activeChat = chatStore.activeChat();
        if (activeChat) {
          activeChat.contextSize = contextSize;
        }
      }
      
      // Update UI to reflect active chat
      updateUIForActiveChat();
    }
    
    // Save initial state
    saveChatsToStorage();
  }, 300);
});

