const connectButton = document.getElementById('connectButton');
const disconnectButton = document.getElementById('disconnectButton');
const testToneButton = document.getElementById('testToneButton');
const diagnosticsButton = document.getElementById('diagnosticsButton');
const audioPlayback = document.getElementById('audioPlayback');
const transcript = document.getElementById('transcript');
const statusIcon = document.getElementById('statusIcon');
const statusLabel = document.getElementById('statusLabel');
const connectionStatus = document.getElementById('connectionStatus');
const modelSelect = document.getElementById('modelSelect');

let pc = null;
let dc = null;
let localStream = null;
let hasError = false;

// Audio diagnostics state
let audioCtx = null;
let analyser = null;
let diagAnimFrame = null;
let statsInterval = null;
let diagVisible = false;

connectButton.addEventListener('click', connect);
disconnectButton.addEventListener('click', disconnect);
testToneButton.addEventListener('click', sendTestTone);
diagnosticsButton.addEventListener('click', toggleDiagnostics);

// Show pipeline details when a model is selected
modelSelect.addEventListener('change', function() {
  const opt = this.options[this.selectedIndex];
  const details = document.getElementById('pipelineDetails');
  if (!opt || !opt.value) {
    details.classList.add('hidden');
    return;
  }
  document.getElementById('pipelineVAD').textContent = opt.dataset.vad || '--';
  document.getElementById('pipelineSTT').textContent = opt.dataset.stt || '--';
  document.getElementById('pipelineLLM').textContent = opt.dataset.llm || '--';
  document.getElementById('pipelineTTS').textContent = opt.dataset.tts || '--';
  details.classList.remove('hidden');

  // Pre-fill voice from model default if the user hasn't typed anything
  const voiceInput = document.getElementById('voiceInput');
  if (!voiceInput.dataset.userEdited) {
    voiceInput.value = opt.dataset.voice || '';
  }
});

// Track if user manually edited the voice field
document.getElementById('voiceInput').addEventListener('input', function() {
  this.dataset.userEdited = 'true';
});

// Auto-select first model on page load
if (modelSelect.options.length > 1) {
  modelSelect.selectedIndex = 1;
  modelSelect.dispatchEvent(new Event('change'));
}

function getModel() {
  return modelSelect.value;
}

function setStatus(state, text) {
  statusLabel.textContent = text || state;
  statusIcon.className = 'fa-solid fa-circle';
  connectionStatus.className = 'rounded-lg p-4 mb-4 flex items-center space-x-3';

  switch (state) {
    case 'disconnected':
      statusIcon.classList.add('text-[var(--color-text-secondary)]');
      connectionStatus.classList.add('bg-[var(--color-bg-primary)]/50', 'border', 'border-[var(--color-border-subtle)]');
      statusLabel.classList.add('text-[var(--color-text-secondary)]');
      break;
    case 'connecting':
      statusIcon.className = 'fa-solid fa-spinner fa-spin text-[var(--color-primary)]';
      connectionStatus.classList.add('bg-[var(--color-primary-light)]', 'border', 'border-[var(--color-primary)]/30');
      statusLabel.className = 'font-medium text-[var(--color-primary)]';
      break;
    case 'connected':
      statusIcon.classList.add('text-[var(--color-success)]');
      connectionStatus.classList.add('bg-[var(--color-success)]/10', 'border', 'border-[var(--color-success)]/30');
      statusLabel.className = 'font-medium text-[var(--color-success)]';
      break;
    case 'listening':
      statusIcon.className = 'fa-solid fa-microphone text-[var(--color-success)]';
      connectionStatus.classList.add('bg-[var(--color-success)]/10', 'border', 'border-[var(--color-success)]/30');
      statusLabel.className = 'font-medium text-[var(--color-success)]';
      break;
    case 'thinking':
      statusIcon.className = 'fa-solid fa-brain fa-beat text-[var(--color-primary)]';
      connectionStatus.classList.add('bg-[var(--color-primary-light)]', 'border', 'border-[var(--color-primary)]/30');
      statusLabel.className = 'font-medium text-[var(--color-primary)]';
      break;
    case 'speaking':
      statusIcon.className = 'fa-solid fa-volume-high fa-beat-fade text-[var(--color-accent)]';
      connectionStatus.classList.add('bg-[var(--color-accent)]/10', 'border', 'border-[var(--color-accent)]/30');
      statusLabel.className = 'font-medium text-[var(--color-accent)]';
      break;
    case 'error':
      statusIcon.classList.add('text-[var(--color-error)]');
      connectionStatus.classList.add('bg-[var(--color-error-light)]', 'border', 'border-[var(--color-error)]/30');
      statusLabel.className = 'font-medium text-[var(--color-error)]';
      break;
  }
}

// Currently streaming assistant message element (for incremental updates)
let streamingEntry = null;

function addTranscript(role, text) {
  // Remove the placeholder if present
  const placeholder = transcript.querySelector('.italic');
  if (placeholder) placeholder.remove();

  const entry = document.createElement('div');
  entry.className = 'flex items-start space-x-2';

  const icon = document.createElement('i');
  const msg = document.createElement('p');
  msg.className = 'text-[var(--color-text-primary)]';
  msg.textContent = text;

  if (role === 'user') {
    icon.className = 'fa-solid fa-user text-[var(--color-primary)] mt-1 flex-shrink-0';
  } else {
    icon.className = 'fa-solid fa-robot text-[var(--color-accent)] mt-1 flex-shrink-0';
  }

  entry.appendChild(icon);
  entry.appendChild(msg);
  transcript.appendChild(entry);
  transcript.scrollTop = transcript.scrollHeight;
  return entry;
}

function updateStreamingTranscript(role, delta) {
  if (!streamingEntry) {
    streamingEntry = addTranscript(role, delta);
  } else {
    const msg = streamingEntry.querySelector('p');
    if (msg) msg.textContent += delta;
    transcript.scrollTop = transcript.scrollHeight;
  }
}

function finalizeStreamingTranscript(role, fullText) {
  if (streamingEntry) {
    const msg = streamingEntry.querySelector('p');
    if (msg) msg.textContent = fullText;
    streamingEntry = null;
  } else {
    addTranscript(role, fullText);
  }
  transcript.scrollTop = transcript.scrollHeight;
}

// Send a session.update event with the user's settings
function sendSessionUpdate() {
  if (!dc || dc.readyState !== 'open') return;

  const instructions = document.getElementById('instructionsInput').value.trim();
  const voice = document.getElementById('voiceInput').value.trim();
  const language = document.getElementById('languageInput').value.trim();

  // Only send if the user configured something
  if (!instructions && !voice && !language) return;

  const session = {};

  if (instructions) {
    session.instructions = instructions;
  }

  if (voice || language) {
    session.audio = {};
    if (voice) {
      session.audio.output = { voice: voice };
    }
    if (language) {
      session.audio.input = {
        transcription: { language: language }
      };
    }
  }

  const event = {
    type: 'session.update',
    session: session,
  };

  console.log('[session.update]', event);
  dc.send(JSON.stringify(event));
}

function handleServerEvent(event) {
  console.log('[event]', event.type, event);

  switch (event.type) {
    case 'session.created':
      // Session is ready — send any user settings
      sendSessionUpdate();
      setStatus('listening', 'Listening...');
      break;

    case 'session.updated':
      console.log('[session.updated] Session settings applied', event.session);
      break;

    case 'input_audio_buffer.speech_started':
      setStatus('listening', 'Hearing you speak...');
      break;

    case 'input_audio_buffer.speech_stopped':
      setStatus('thinking', 'Processing...');
      break;

    case 'conversation.item.input_audio_transcription.completed':
      if (event.transcript) {
        addTranscript('user', event.transcript);
      }
      setStatus('thinking', 'Generating response...');
      break;

    case 'response.output_audio_transcript.delta':
      // Incremental transcript — update the in-progress assistant message
      if (event.delta) {
        updateStreamingTranscript('assistant', event.delta);
      }
      break;

    case 'response.output_audio_transcript.done':
      if (event.transcript) {
        finalizeStreamingTranscript('assistant', event.transcript);
      }
      break;

    case 'response.output_audio.delta':
      setStatus('speaking', 'Speaking...');
      break;

    case 'response.done':
      setStatus('listening', 'Listening...');
      break;

    case 'error':
      console.error('Server error:', event.error);
      hasError = true;
      setStatus('error', 'Error: ' + (event.error?.message || 'Unknown error'));
      break;
  }
}

async function connect() {
  const model = getModel();
  if (!model) {
    alert('Please select a pipeline model first.');
    return;
  }

  if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
    setStatus('error', 'Microphone access requires HTTPS or localhost.');
    return;
  }

  setStatus('connecting', 'Connecting...');
  connectButton.style.display = 'none';
  disconnectButton.style.display = '';
  testToneButton.style.display = '';
  diagnosticsButton.style.display = '';

  try {
    // Get microphone access
    localStream = await navigator.mediaDevices.getUserMedia({ audio: true });

    // Create peer connection
    pc = new RTCPeerConnection({});

    // Add local audio track
    for (const track of localStream.getAudioTracks()) {
      pc.addTrack(track, localStream);
    }

    // Handle remote audio track (server's TTS output)
    pc.ontrack = (event) => {
      audioPlayback.srcObject = event.streams[0];
      // If diagnostics panel is open, start analyzing the new stream
      if (diagVisible) startDiagnostics();
    };

    // Create the events data channel (client must create it so m=application
    // is included in the SDP offer — the answerer cannot add new m-lines)
    dc = pc.createDataChannel('oai-events');
    dc.onmessage = (msg) => {
      try {
        const text = typeof msg.data === 'string'
          ? msg.data
          : new TextDecoder().decode(msg.data);
        const event = JSON.parse(text);
        handleServerEvent(event);
      } catch (e) {
        console.error('Failed to parse server event:', e);
      }
    };
    dc.onclose = () => {
      console.log('Data channel closed');
    };

    pc.onconnectionstatechange = () => {
      console.log('Connection state:', pc.connectionState);
      if (pc.connectionState === 'connected') {
        setStatus('connected', 'Connected, waiting for session...');
      } else if (pc.connectionState === 'failed' || pc.connectionState === 'closed') {
        disconnect();
      }
    };

    // Create offer
    const offer = await pc.createOffer();
    await pc.setLocalDescription(offer);

    // Wait for ICE gathering
    await new Promise((resolve) => {
      if (pc.iceGatheringState === 'complete') {
        resolve();
      } else {
        pc.onicegatheringstatechange = () => {
          if (pc.iceGatheringState === 'complete') resolve();
        };
        // Timeout after 5s
        setTimeout(resolve, 5000);
      }
    });

    // Send offer to server
    const response = await fetch('v1/realtime/calls', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({
        sdp: pc.localDescription.sdp,
        model: model,
      }),
    });

    if (!response.ok) {
      const err = await response.json().catch(() => ({ error: 'Unknown error' }));
      throw new Error(err.error || `HTTP ${response.status}`);
    }

    const data = await response.json();

    // Set remote description (server's answer)
    await pc.setRemoteDescription({
      type: 'answer',
      sdp: data.sdp,
    });

    console.log('WebRTC connection established, session:', data.session_id);
  } catch (err) {
    console.error('Connection failed:', err);
    hasError = true;
    setStatus('error', 'Connection failed: ' + err.message);
    disconnect();
  }
}

function sendTestTone() {
  if (!dc || dc.readyState !== 'open') {
    console.warn('Data channel not open');
    return;
  }
  console.log('[test-tone] Requesting server test tone...');
  dc.send(JSON.stringify({ type: 'test_tone' }));
  addTranscript('assistant', '(Test tone requested — you should hear a 440 Hz beep)');
}

function disconnect() {
  stopDiagnostics();
  if (dc) {
    dc.close();
    dc = null;
  }
  if (pc) {
    pc.close();
    pc = null;
  }
  if (localStream) {
    localStream.getTracks().forEach(t => t.stop());
    localStream = null;
  }
  audioPlayback.srcObject = null;

  if (!hasError) {
    setStatus('disconnected', 'Disconnected');
  }
  hasError = false;
  connectButton.style.display = '';
  disconnectButton.style.display = 'none';
  testToneButton.style.display = 'none';
  diagnosticsButton.style.display = 'none';
}

// ── Audio Diagnostics ──

function toggleDiagnostics() {
  const panel = document.getElementById('diagnosticsPanel');
  diagVisible = !diagVisible;
  panel.style.display = diagVisible ? '' : 'none';
  if (diagVisible) {
    startDiagnostics();
  } else {
    stopDiagnostics();
  }
}

function startDiagnostics() {
  if (!audioPlayback.srcObject) return;

  // Create AudioContext and connect the remote stream to an AnalyserNode
  if (!audioCtx) {
    audioCtx = new AudioContext();
    const source = audioCtx.createMediaStreamSource(audioPlayback.srcObject);
    analyser = audioCtx.createAnalyser();
    analyser.fftSize = 8192;
    analyser.smoothingTimeConstant = 0.3;
    source.connect(analyser);

    document.getElementById('statSampleRate').textContent = audioCtx.sampleRate + ' Hz';
  }

  // Start rendering loop
  if (!diagAnimFrame) {
    drawDiagnostics();
  }

  // Start WebRTC stats polling
  if (!statsInterval) {
    pollWebRTCStats();
    statsInterval = setInterval(pollWebRTCStats, 1000);
  }
}

function stopDiagnostics() {
  if (diagAnimFrame) {
    cancelAnimationFrame(diagAnimFrame);
    diagAnimFrame = null;
  }
  if (statsInterval) {
    clearInterval(statsInterval);
    statsInterval = null;
  }
  if (audioCtx) {
    audioCtx.close();
    audioCtx = null;
    analyser = null;
  }
}

function drawDiagnostics() {
  if (!analyser || !diagVisible) {
    diagAnimFrame = null;
    return;
  }

  diagAnimFrame = requestAnimationFrame(drawDiagnostics);

  // ── Waveform ──
  const waveCanvas = document.getElementById('waveformCanvas');
  const wCtx = waveCanvas.getContext('2d');
  const timeData = new Float32Array(analyser.fftSize);
  analyser.getFloatTimeDomainData(timeData);

  const w = waveCanvas.width;
  const h = waveCanvas.height;
  wCtx.fillStyle = '#000';
  wCtx.fillRect(0, 0, w, h);
  wCtx.strokeStyle = '#0f0';
  wCtx.lineWidth = 1;
  wCtx.beginPath();
  const sliceWidth = w / timeData.length;
  let x = 0;
  for (let i = 0; i < timeData.length; i++) {
    const y = (1 - timeData[i]) * h / 2;
    if (i === 0) wCtx.moveTo(x, y);
    else wCtx.lineTo(x, y);
    x += sliceWidth;
  }
  wCtx.stroke();

  // Compute RMS
  let sumSq = 0;
  for (let i = 0; i < timeData.length; i++) sumSq += timeData[i] * timeData[i];
  const rms = Math.sqrt(sumSq / timeData.length);
  const rmsDb = rms > 0 ? (20 * Math.log10(rms)).toFixed(1) : '-Inf';
  document.getElementById('statRMS').textContent = rmsDb + ' dBFS';

  // ── FFT Spectrum ──
  const specCanvas = document.getElementById('spectrumCanvas');
  const sCtx = specCanvas.getContext('2d');
  const freqData = new Float32Array(analyser.frequencyBinCount);
  analyser.getFloatFrequencyData(freqData);

  const sw = specCanvas.width;
  const sh = specCanvas.height;
  sCtx.fillStyle = '#000';
  sCtx.fillRect(0, 0, sw, sh);

  // Draw spectrum (0 to 4kHz range for speech/tone analysis)
  const sampleRate = audioCtx.sampleRate;
  const binHz = sampleRate / analyser.fftSize;
  const maxFreqDisplay = 4000;
  const maxBin = Math.min(Math.ceil(maxFreqDisplay / binHz), freqData.length);
  const barWidth = sw / maxBin;

  sCtx.fillStyle = '#0cf';
  let peakBin = 0;
  let peakVal = -Infinity;
  for (let i = 0; i < maxBin; i++) {
    const db = freqData[i];
    if (db > peakVal) {
      peakVal = db;
      peakBin = i;
    }
    // Map dB (-100 to 0) to pixel height
    const barH = Math.max(0, ((db + 100) / 100) * sh);
    sCtx.fillRect(i * barWidth, sh - barH, Math.max(1, barWidth - 0.5), barH);
  }

  // Draw frequency labels
  sCtx.fillStyle = '#888';
  sCtx.font = '10px monospace';
  for (let f = 500; f <= maxFreqDisplay; f += 500) {
    const xPos = (f / binHz) * barWidth;
    sCtx.fillText(f + '', xPos - 10, sh - 2);
  }

  // Mark 440 Hz
  const bin440 = Math.round(440 / binHz);
  const x440 = bin440 * barWidth;
  sCtx.strokeStyle = '#f00';
  sCtx.lineWidth = 1;
  sCtx.beginPath();
  sCtx.moveTo(x440, 0);
  sCtx.lineTo(x440, sh);
  sCtx.stroke();
  sCtx.fillStyle = '#f00';
  sCtx.fillText('440', x440 + 2, 10);

  const peakFreq = peakBin * binHz;
  document.getElementById('statPeakFreq').textContent =
    peakFreq.toFixed(0) + ' Hz (' + peakVal.toFixed(1) + ' dB)';

  // Compute THD (Total Harmonic Distortion) relative to 440 Hz
  // THD = sqrt(sum of harmonic powers / fundamental power)
  const fundamentalBin = Math.round(440 / binHz);
  const fundamentalPower = Math.pow(10, freqData[fundamentalBin] / 10);
  let harmonicPower = 0;
  for (let h = 2; h <= 10; h++) {
    const hBin = Math.round(440 * h / binHz);
    if (hBin < freqData.length) {
      harmonicPower += Math.pow(10, freqData[hBin] / 10);
    }
  }
  const thd = fundamentalPower > 0
    ? (Math.sqrt(harmonicPower / fundamentalPower) * 100).toFixed(1)
    : '--';
  document.getElementById('statTHD').textContent = thd + '%';
}

async function pollWebRTCStats() {
  if (!pc) return;
  try {
    const stats = await pc.getStats();
    const raw = [];
    stats.forEach((report) => {
      if (report.type === 'inbound-rtp' && report.kind === 'audio') {
        document.getElementById('statPacketsRecv').textContent =
          report.packetsReceived ?? '--';
        document.getElementById('statPacketsLost').textContent =
          report.packetsLost ?? '--';
        document.getElementById('statJitter').textContent =
          report.jitter !== undefined ? (report.jitter * 1000).toFixed(1) + ' ms' : '--';
        document.getElementById('statConcealed').textContent =
          report.concealedSamples ?? '--';

        raw.push('── inbound-rtp (audio) ──');
        raw.push('  packetsReceived: ' + report.packetsReceived);
        raw.push('  packetsLost: ' + report.packetsLost);
        raw.push('  jitter: ' + (report.jitter !== undefined ? (report.jitter * 1000).toFixed(2) + ' ms' : 'N/A'));
        raw.push('  bytesReceived: ' + report.bytesReceived);
        raw.push('  concealedSamples: ' + report.concealedSamples);
        raw.push('  silentConcealedSamples: ' + report.silentConcealedSamples);
        raw.push('  totalSamplesReceived: ' + report.totalSamplesReceived);
        raw.push('  insertedSamplesForDecel: ' + report.insertedSamplesForDeceleration);
        raw.push('  removedSamplesForAccel: ' + report.removedSamplesForAcceleration);
        raw.push('  jitterBufferDelay: ' + (report.jitterBufferDelay !== undefined ? report.jitterBufferDelay.toFixed(3) + ' s' : 'N/A'));
        raw.push('  jitterBufferTargetDelay: ' + (report.jitterBufferTargetDelay !== undefined ? report.jitterBufferTargetDelay.toFixed(3) + ' s' : 'N/A'));
        raw.push('  jitterBufferEmittedCount: ' + report.jitterBufferEmittedCount);
      }
    });
    document.getElementById('statsRaw').textContent = raw.join('\n');
  } catch (e) {
    console.warn('Stats polling error:', e);
  }
}
