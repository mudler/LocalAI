
const recordButton = document.getElementById('recordButton');
const audioPlayback = document.getElementById('audioPlayback');
const resetButton = document.getElementById('resetButton');

let mediaRecorder;
let audioChunks = [];
let isRecording = false;
let conversationHistory = [];
let resetTimer;

function getApiKey() {
    return document.getElementById('apiKey').value;
}

function getModel() {
    return document.getElementById('modelSelect').value;
}

function getWhisperModel() {
    return document.getElementById('whisperModelSelect').value;
}

function getTTSModel() {
    return document.getElementById('ttsModelSelect').value;
}

function resetConversation() {
    conversationHistory = [];
    console.log("Conversation has been reset.");
    clearTimeout(resetTimer);
}

function setResetTimer() {
    clearTimeout(resetTimer);
    resetTimer = setTimeout(resetConversation, 300000); // Reset after 5 minutes
}

recordButton.addEventListener('click', toggleRecording);
resetButton.addEventListener('click', resetConversation);

function toggleRecording() {
    if (!isRecording) {
        startRecording();
    } else {
        stopRecording();
    }
}

async function startRecording() {
    if (!navigator.mediaDevices) {
        alert('MediaDevices API not supported!');
        return;
    }
    const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    mediaRecorder = new MediaRecorder(stream);
    audioChunks = [];
    mediaRecorder.ondataavailable = (event) => {
        audioChunks.push(event.data);
    };
    mediaRecorder.start();
    recordButton.textContent = 'Stop Recording';
    isRecording = true;
}

function stopRecording() {
    mediaRecorder.stop();
    mediaRecorder.onstop = async () => {
        document.getElementById("loader").style.display = "block";
        const audioBlob = new Blob(audioChunks, { type: 'audio/webm' });
        const transcript = await sendAudioToWhisper(audioBlob);
        console.log("Transcript:", transcript)
        const responseText = await sendTextToChatGPT(transcript);
        console.log("Response:", responseText)

        const ttsAudio = await getTextToSpeechAudio(responseText);
        playAudioResponse(ttsAudio);

        recordButton.textContent = 'Record';
        isRecording = false;
        document.getElementById("loader").style.display = "none";
    };
}

function submitKey(event) {
    event.preventDefault();
    localStorage.setItem("key", document.getElementById("apiKey").value);
    document.getElementById("apiKey").blur();
}

document.getElementById("key").addEventListener("submit", submitKey);


storeKey = localStorage.getItem("key");
if (storeKey) {
  document.getElementById("apiKey").value = storeKey;
} else {
  document.getElementById("apiKey").value = null;
}


async function sendAudioToWhisper(audioBlob) {
    const formData = new FormData();
    formData.append('file', audioBlob);
    formData.append('model', getWhisperModel());
    API_KEY = localStorage.getItem("key");

    const response = await fetch('/v1/audio/transcriptions', {
        method: 'POST',
        headers: {
            'Authorization': `Bearer ${API_KEY}`
        },
        body: formData
    });

    const result = await response.json();
    console.log("Whisper result:", result)
    return result.text;
}

async function sendTextToChatGPT(text) {
    conversationHistory.push({ role: "user", content: text });
    API_KEY = localStorage.getItem("key");

    const response = await fetch('/v1/chat/completions', {
        method: 'POST',
        headers: {
            'Authorization': `Bearer ${API_KEY}`,
            'Content-Type': 'application/json'
        },
        body: JSON.stringify({
            model: getModel(),
            messages: conversationHistory
        })
    });

    const result = await response.json();
    const responseText = result.choices[0].message.content;
    conversationHistory.push({ role: "assistant", content: responseText });

    setResetTimer();

    return responseText;
}

async function getTextToSpeechAudio(text) {
    API_KEY = localStorage.getItem("key");

    const response = await fetch('/v1/audio/speech', {
        
        method: 'POST',
        headers: {
            'Authorization': `Bearer ${API_KEY}`,
            'Content-Type': 'application/json'
        },
        body: JSON.stringify({ 
          //  "backend": "string",
            input: text,
            model: getTTSModel(),
           // "voice": "string"
         })
    });

    const audioBlob = await response.blob();
    return audioBlob;  // Return the blob directly
}

function playAudioResponse(audioBlob) {
    const audioUrl = URL.createObjectURL(audioBlob);
    audioPlayback.src = audioUrl;
    audioPlayback.hidden = false;
    audioPlayback.play();
}

