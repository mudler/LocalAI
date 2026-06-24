function showNotification(type, message) {
  const existing = document.getElementById('sound-notification');
  if (existing) existing.remove();

  const notification = document.createElement('div');
  notification.id = 'sound-notification';
  notification.className = 'fixed top-24 right-4 z-50 p-4 rounded-lg shadow-lg flex items-center gap-2 transition-all duration-300';
  if (type === 'error') {
    notification.classList.add('bg-red-900/90', 'border', 'border-red-700', 'text-red-200');
    notification.innerHTML = '<i class="fas fa-circle-exclamation text-red-400 mr-2"></i>' + message;
  } else {
    notification.classList.add('bg-green-900/90', 'border', 'border-green-700', 'text-green-200');
    notification.innerHTML = '<i class="fas fa-circle-check text-green-400 mr-2"></i>' + message;
  }
  document.body.appendChild(notification);
  setTimeout(function() {
    if (document.getElementById('sound-notification')) {
      document.getElementById('sound-notification').remove();
    }
  }, 5000);
}

function buildRequestBody() {
  const model = document.getElementById('sound-model').value;
  const body = { model_id: model };
  const isSimple = document.getElementById('mode-simple').checked;

  if (isSimple) {
    const text = document.getElementById('text').value.trim();
    if (text) body.text = text;
    body.instrumental = document.getElementById('instrumental').checked;
    const vocal = document.getElementById('vocal_language').value.trim();
    if (vocal) body.vocal_language = vocal;
  } else {
    // Advanced mode: do NOT send 'text' field - it triggers simple mode in backend
    // Only send caption, lyrics, and other advanced fields
    const caption = document.getElementById('caption').value.trim();
    if (caption) body.caption = caption;
    const lyrics = document.getElementById('lyrics').value.trim();
    if (lyrics) body.lyrics = lyrics;
    body.think = document.getElementById('think').checked;
    const bpm = document.getElementById('bpm').value.trim();
    if (bpm) body.bpm = parseInt(bpm, 10);
    const duration = document.getElementById('duration_seconds').value.trim();
    if (duration) body.duration_seconds = parseFloat(duration);
    const keyscale = document.getElementById('keyscale').value.trim();
    if (keyscale) body.keyscale = keyscale;
    const language = document.getElementById('language').value.trim();
    if (language) body.language = language;
    const timesignature = document.getElementById('timesignature').value.trim();
    if (timesignature) body.timesignature = timesignature;
  }
  return body;
}

async function generateSound(event) {
  event.preventDefault();

  const isSimple = document.getElementById('mode-simple').checked;
  if (isSimple) {
    const text = document.getElementById('text').value.trim();
    if (!text) {
      showNotification('error', 'Please enter text (description)');
      return;
    }
  } else {
    // Advanced mode: only check caption and lyrics (text field is hidden and not used)
    const caption = document.getElementById('caption').value.trim();
    const lyrics = document.getElementById('lyrics').value.trim();
    if (!caption && !lyrics) {
      showNotification('error', 'Please enter at least caption or lyrics');
      return;
    }
  }

  const loader = document.getElementById('loader');
  const resultDiv = document.getElementById('result');
  const generateBtn = document.getElementById('generate-btn');

  loader.style.display = 'block';
  generateBtn.disabled = true;
  resultDiv.innerHTML = '<p class="text-[var(--color-text-secondary)] italic">Generating sound...</p>';

  try {
    const response = await fetch('v1/sound-generation', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(buildRequestBody()),
    });

    if (!response.ok) {
      let errMsg = 'Request failed';
      const ct = response.headers.get('content-type');
      if (ct && ct.indexOf('application/json') !== -1) {
        const json = await response.json();
        if (json && json.error && json.error.message) errMsg = json.error.message;
      }
      resultDiv.innerHTML = '<div class="text-red-400 flex items-center gap-2"><i class="fas fa-circle-exclamation"></i> ' + errMsg + '</div>';
      showNotification('error', 'Failed to generate sound');
      return;
    }

    const blob = await response.blob();
    const audioUrl = window.URL.createObjectURL(blob);

    const wrap = document.createElement('div');
    wrap.className = 'flex flex-col items-center gap-4 w-full';

    const audio = document.createElement('audio');
    audio.controls = true;
    audio.src = audioUrl;
    audio.className = 'w-full max-w-md';

    const actions = document.createElement('div');
    actions.className = 'flex flex-wrap justify-center gap-3';

    const downloadLink = document.createElement('a');
    downloadLink.href = audioUrl;
    downloadLink.download = 'sound-' + new Date().toISOString().slice(0, 10) + '.wav';
    downloadLink.className = 'inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-[var(--color-primary)] text-[var(--color-bg-primary)] hover:opacity-90 transition';
    downloadLink.innerHTML = '<i class="fas fa-download"></i> Download';

    actions.appendChild(downloadLink);
    wrap.appendChild(audio);
    wrap.appendChild(actions);
    resultDiv.innerHTML = '';
    resultDiv.appendChild(wrap);

    audio.play().catch(function() {});
    showNotification('success', 'Sound generated successfully');
  } catch (err) {
    console.error('Sound generation error:', err);
    resultDiv.innerHTML = '<div class="text-red-400 flex items-center gap-2"><i class="fas fa-circle-exclamation"></i> Network error</div>';
    showNotification('error', 'Network error');
  } finally {
    loader.style.display = 'none';
    generateBtn.disabled = false;
  }
}

document.addEventListener('DOMContentLoaded', function() {
  document.getElementById('sound-form').addEventListener('submit', generateSound);
  document.getElementById('loader').style.display = 'none';
});
