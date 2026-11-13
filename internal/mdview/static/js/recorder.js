// recorder.js — Worker for audio recording + document reference event timeline
//
// Starts/stops microphone recording via the toolbar record button and, while recording, logs the user's
//   - file views (open)
//   - text copies (copy: Ctrl+C / copy button)
//   - line/block clicks (focus)
//   - word picks (word: double-click — the selected text, or else the first word after the click point)
// against the audio timeline (t: ms since recording start).
// On stop, uploads the audio and event metadata (JSON) to the server (/api/recording),
// which saves them under mdview/mdview-recordings/rec-<timestamp>/ in the user's app data.
//
// Line number policy:
//   - Code viewer: the file's actual line numbers (computed from click coordinates/selection)
//   - Markdown: per-block line ranges. If SmartMD preprocessing shifted lines, the same slice is
//     searched for in the original text to correct them; on failure the range is marked approx.
//   - PDF: instead of lines, the pdf field records the page number and click coordinates (x, y)
//     normalized to 0–1 from the page's top-left corner (selection/copy records only the page number).

const API_RECORDING_PATH = '/api/recording';
const API_RECORDING_STATUS_PATH = '/api/recording/status';
const API_RECORDING_FILE_PATH = '/api/recording/file';
const STATUS_POLL_MS = 2000;
const STATUS_POLL_MAX_MS = 10 * 60 * 1000;
const JOB_DONE_HIDE_MS = 8000;
const DEVICE_STORE_KEY = 'mdview-mic-device';
const TIMER_INTERVAL_MS = 200;
const FOCUS_TEXT_LIMIT = 2000;
const ERROR_TEXT_LIMIT = 200;
const AUDIO_TIMESLICE_MS = 1000;
const TOAST_HIDE_DELAY_MS = 6000;
const OBJECT_URL_REVOKE_DELAY_MS = 10000;
const PDF_COORD_DECIMALS = 10000;
const PDF_TEXT_SNAP_RADIUS_PX = 160;

// Per-browser support priority (Chrome/Edge: webm+opus, Safari: mp4)
const MIME_CANDIDATES = [
  'audio/webm;codecs=opus',
  'audio/webm',
  'audio/mp4',
  'audio/ogg;codecs=opus',
];

const DOWNLOAD_EXT_BY_MIME = { 'audio/webm': 'webm', 'audio/mp4': 'm4a', 'audio/ogg': 'ogg' };

let ctx = null; // injected by app.js: { preview, getState() → {mode, root, file, raw, transformed, blocks} }
let ui = null;

let recording = false;
let mediaRecorder = null;
let mediaStream = null;
let chunks = [];
let events = [];
let t0 = 0;
let startedAt = '';
let timerId = null;
let lastFailed = null; // holds {blob, metaJson} on upload failure (for the download fallback)

let toastEl = null;
let toastTimer = null;
let jobTimer = null;         // transcription status polling timer
let activeDeviceLabel = '';  // name of the input device used for the current recording
let uploading = false;     // whether a previous recording is uploading (blocks new recordings until the upload finishes)

/**
 * Initializes the recording feature.
 * @param {{preview: HTMLElement, getState: () => object}} context
 */
export function initRecorder(context) {
  ctx = context;
  ui = {
    button: document.getElementById('record-button'),
    status: document.getElementById('record-status'),
    timer: document.getElementById('record-timer'),
    eventsCount: document.getElementById('record-events'),
    levelBar: document.getElementById('record-level-bar'),
    deviceSelect: document.getElementById('record-device'),
    jobStatus: document.getElementById('job-status'),
  };
  ui.button.addEventListener('click', toggleRecording);
  document.addEventListener('copy', handleCopy);
  document.addEventListener('mdview:manualcopy', handleManualCopy);
  ctx.preview.addEventListener('click', handleClick);
  ctx.preview.addEventListener('dblclick', handleDblClick);
  window.addEventListener('beforeunload', handleBeforeUnload);

  // Audio input device selection
  if (ui.deviceSelect) {
    ui.deviceSelect.addEventListener('change', () => {
      try {
        localStorage.setItem(DEVICE_STORE_KEY, ui.deviceSelect.value);
      } catch {
        /* ignore environments without localStorage */
      }
    });
    populateDevices();
    if (navigator.mediaDevices && navigator.mediaDevices.addEventListener) {
      navigator.mediaDevices.addEventListener('devicechange', populateDevices);
    }
  }
}

// ---------------------------------------------------------------------------
// Audio input device selection
// ---------------------------------------------------------------------------

function savedDeviceId() {
  try {
    return localStorage.getItem(DEVICE_STORE_KEY) || '';
  } catch {
    return '';
  }
}

/**
 * Updates the dropdown with the list of input devices.
 * Before microphone permission is granted the browser returns empty labels, so devices show as "Microphone N";
 * this is called again after the first recording (permission granted) to fill in the real device names.
 */
async function populateDevices() {
  if (!ui.deviceSelect || !navigator.mediaDevices || !navigator.mediaDevices.enumerateDevices) return;
  let devices = [];
  try {
    devices = await navigator.mediaDevices.enumerateDevices();
  } catch {
    return;
  }
  const saved = savedDeviceId();
  ui.deviceSelect.replaceChildren();

  const def = document.createElement('option');
  def.value = '';
  def.textContent = 'Default microphone';
  ui.deviceSelect.appendChild(def);

  let index = 0;
  let savedFound = saved === '';
  for (const d of devices) {
    if (d.kind !== 'audioinput') continue;
    if (!d.deviceId || d.deviceId === 'default' || d.deviceId === 'communications') continue;
    index += 1;
    const opt = document.createElement('option');
    opt.value = d.deviceId;
    opt.textContent = d.label || `Microphone ${index}`;
    if (d.deviceId === saved) {
      opt.selected = true;
      savedFound = true;
    }
    ui.deviceSelect.appendChild(opt);
  }
  if (!savedFound) ui.deviceSelect.value = '';
}

/** Called when a file is opened (logged as an open event while recording). */
export function noteFileOpen(file) {
  if (!recording || !file) return;
  pushEvent({ type: 'open', file });
}

function toggleRecording() {
  if (uploading) {
    showToast('The previous recording is still uploading. Please try again shortly.', { error: true });
    return;
  }
  if (recording) stopRecording();
  else startRecording();
}

// ---------------------------------------------------------------------------
// Microphone permission diagnostics
// ---------------------------------------------------------------------------

const MIC_DENIED_GUIDE =
  'Microphone access is blocked for this site.\n' +
  '· Set the microphone to "Allow" via the site permissions icon (lock/settings) in the address bar, then reload\n' +
  '· macOS: enable your browser under System Settings → Privacy & Security → Microphone, then restart the browser';

/** Uses the Permissions API to check whether the microphone is already blocked (false on unsupported browsers). */
async function micPermissionDenied() {
  if (!navigator.permissions || !navigator.permissions.query) return false;
  try {
    const status = await navigator.permissions.query({ name: 'microphone' });
    return status.state === 'denied';
  } catch {
    return false; // Safari etc. don't support 'microphone' → determined by getUserMedia
  }
}

/** Builds a help message for each getUserMedia failure cause. */
function micErrorMessage(err) {
  switch (err.name) {
    case 'NotAllowedError':
    case 'PermissionDeniedError':
      return MIC_DENIED_GUIDE;
    case 'NotFoundError':
    case 'DevicesNotFoundError':
      return 'No usable microphone found. Check that an input device is connected.';
    case 'NotReadableError':
    case 'TrackStartError':
      return 'The microphone is in use by another app and cannot be accessed. Quit that app and try again.';
    default:
      return `Unable to open the microphone: ${err.name} — ${err.message}`;
  }
}

async function startRecording() {
  if (!navigator.mediaDevices || !window.MediaRecorder) {
    showToast(
      'This browser does not support audio recording.\n' +
        `(a secure context is required — make sure the address is localhost: ${location.origin})`,
      { error: true }
    );
    return;
  }
  // If already "blocked", getUserMedia fails without a prompt, so show guidance first.
  if (await micPermissionDenied()) {
    showToast(MIC_DENIED_GUIDE, { error: true });
    return;
  }
  const deviceId = ui.deviceSelect ? ui.deviceSelect.value : '';
  let stream;
  try {
    stream = await navigator.mediaDevices.getUserMedia(
      deviceId ? { audio: { deviceId: { exact: deviceId } } } : { audio: true }
    );
  } catch (err) {
    // The selected device was unplugged or disappeared: fall back to the default microphone
    if (deviceId && (err.name === 'OverconstrainedError' || err.name === 'NotFoundError')) {
      try {
        stream = await navigator.mediaDevices.getUserMedia({ audio: true });
        showToast('The selected microphone was not found; recording with the default microphone.');
        if (ui.deviceSelect) ui.deviceSelect.value = '';
      } catch (err2) {
        showToast(micErrorMessage(err2), { error: true });
        return;
      }
    } else {
      showToast(micErrorMessage(err), { error: true });
      return;
    }
  }
  // Device labels are filled in once permission is granted, so refresh the list.
  populateDevices();
  const track = stream.getAudioTracks()[0];
  activeDeviceLabel = track ? track.label : '';
  const mimeType = MIME_CANDIDATES.find((m) => MediaRecorder.isTypeSupported(m));
  try {
    mediaRecorder = new MediaRecorder(stream, mimeType ? { mimeType } : undefined);
  } catch (err) {
    stream.getTracks().forEach((t) => t.stop());
    showToast(`Unable to start recording: ${err.message}`, { error: true });
    return;
  }
  mediaStream = stream;
  chunks = [];
  events = [];
  startedAt = new Date().toISOString();
  t0 = performance.now();
  recording = true;

  mediaRecorder.addEventListener('dataavailable', (e) => {
    if (e.data && e.data.size > 0) chunks.push(e.data);
  });
  mediaRecorder.addEventListener('stop', finalizeRecording);
  mediaRecorder.addEventListener('error', (e) => {
    showToast(`An error occurred while recording: ${e.error ? e.error.message : e.type}`, { error: true });
  });
  mediaRecorder.start(AUDIO_TIMESLICE_MS);

  startLevelMeter(stream);
  hideToast();
  setRecordingUI(true);
  const state = ctx.getState();
  if (state.file) pushEvent({ type: 'open', file: state.file });
}

// ---------------------------------------------------------------------------
// Input level meter — visual check that the microphone is actually picking up sound
// ---------------------------------------------------------------------------

let audioCtx = null;
let levelRaf = 0;

function startLevelMeter(stream) {
  try {
    const Ctx = window.AudioContext || window.webkitAudioContext;
    if (!Ctx || !ui.levelBar) return;
    audioCtx = new Ctx();
    const analyser = audioCtx.createAnalyser();
    analyser.fftSize = 512;
    audioCtx.createMediaStreamSource(stream).connect(analyser);
    const buf = new Uint8Array(analyser.fftSize);
    const tick = () => {
      analyser.getByteTimeDomainData(buf);
      let peak = 0;
      for (let i = 0; i < buf.length; i++) {
        const dev = Math.abs(buf[i] - 128);
        if (dev > peak) peak = dev;
      }
      ui.levelBar.style.width = `${Math.min(100, Math.round((peak / 128) * 140))}%`;
      levelRaf = requestAnimationFrame(tick);
    };
    tick();
  } catch {
    /* a meter failure does not affect the recording itself */
  }
}

function stopLevelMeter() {
  if (levelRaf) {
    cancelAnimationFrame(levelRaf);
    levelRaf = 0;
  }
  if (audioCtx) {
    audioCtx.close().catch(() => {});
    audioCtx = null;
  }
  if (ui.levelBar) ui.levelBar.style.width = '0';
}

function stopRecording() {
  if (!mediaRecorder || mediaRecorder.state === 'inactive') return;
  mediaRecorder.stop(); // → finalizeRecording runs on the 'stop' event
}

function finalizeRecording() {
  const durationMs = elapsedMs();
  recording = false;
  stopLevelMeter();
  if (mediaStream) {
    mediaStream.getTracks().forEach((t) => t.stop());
    mediaStream = null;
  }
  setRecordingUI(false);
  clearFocusMarks();

  const type = (mediaRecorder && mediaRecorder.mimeType) || 'audio/webm';
  const blob = new Blob(chunks, { type });
  mediaRecorder = null;
  chunks = [];
  if (blob.size === 0) {
    showToast(
      'No audio data was recorded. While recording, check that the input level at the top is moving.\n' +
        '(macOS: check that the browser is allowed under System Settings → Privacy & Security → Microphone)',
      { error: true }
    );
    return;
  }
  // Finalize the metadata before waiting, because a new recording started meanwhile would reset events, startedAt and the device.
  const meta = buildMeta(durationMs);
  uploadRecording(blob, meta);
}

function buildMeta(durationMs) {
  const state = ctx.getState();
  const meta = {
    version: 1,
    tool: 'mdview',
    mode: state.mode,
    startedAt,
    durationMs,
    events,
  };
  if (state.mode === 'folder') meta.root = state.root;
  else meta.file = state.file;
  if (activeDeviceLabel) meta.device = activeDeviceLabel;
  return meta;
}

async function uploadRecording(blob, meta) {
  const metaJson = JSON.stringify(meta);
  const form = new FormData();
  form.append('audio', blob, 'audio');
  form.append('meta', metaJson);
  uploading = true;
  ui.button.disabled = true;
  setJobStatus('Uploading', { spin: true });
  try {
    const res = await fetch(API_RECORDING_PATH, { method: 'POST', body: form });
    if (!res.ok) {
      const detail = (await res.text()).slice(0, ERROR_TEXT_LIMIT);
      throw new Error(detail || res.statusText);
    }
    const result = await res.json();
    lastFailed = null;
    setJobStatus('Post-processing', { spin: true });
    pollTranscribeStatus(result.id);
    showToast(
      `Recording saved (${meta.events.length} events)\n${result.dir}\n` +
        'Post-processing (transcription, prompt.txt generation) is running in the background (you will be notified when done)'
    );
  } catch (err) {
    setJobStatus('');
    lastFailed = { blob, metaJson };
    showToast(`Failed to upload the recording: ${err.message}`, { error: true, download: true });
  } finally {
    uploading = false;
    ui.button.disabled = false;
  }
}

// ---------------------------------------------------------------------------
// Transcription progress (toolbar chip + server polling)
// ---------------------------------------------------------------------------

/** Updates the toolbar job status chip. Hidden when the text is empty. */
function setJobStatus(text, { spin = false, done = false } = {}) {
  if (!ui.jobStatus) return;
  if (jobTimer) {
    clearTimeout(jobTimer);
    jobTimer = null;
  }
  if (!text) {
    ui.jobStatus.hidden = true;
    ui.jobStatus.replaceChildren();
    return;
  }
  ui.jobStatus.hidden = false;
  ui.jobStatus.classList.toggle('done', done);
  ui.jobStatus.replaceChildren();
  if (spin) {
    const spinner = document.createElement('span');
    spinner.className = 'job-spinner';
    spinner.setAttribute('aria-hidden', 'true');
    ui.jobStatus.appendChild(spinner);
  }
  if (done) {
    const check = document.createElement('span');
    check.textContent = '✓';
    ui.jobStatus.appendChild(check);
  }
  const label = document.createElement('span');
  label.textContent = text;
  ui.jobStatus.appendChild(label);
}

/** Periodically polls automatic transcription status and reports completion/failure. */
function pollTranscribeStatus(id) {
  const startedAtMs = Date.now();
  const tick = async () => {
    let job = null;
    try {
      const res = await fetch(`${API_RECORDING_STATUS_PATH}?id=${encodeURIComponent(id)}`);
      if (res.ok) job = await res.json();
    } catch {
      /* transient network error — retry on the next poll */
    }
    if (job && job.state === 'done') {
      setJobStatus('Post-processing done', { done: true });
      setTimeout(() => setJobStatus(''), JOB_DONE_HIDE_MS);
      notifyTranscriptReady(job);
      return;
    }
    if (job && job.state === 'error') {
      setJobStatus('');
      showToast(`Post-processing failed: ${(job.error || '').slice(0, ERROR_TEXT_LIMIT)}`, { error: true });
      return;
    }
    if (Date.now() - startedAtMs > STATUS_POLL_MAX_MS) {
      setJobStatus('');
      return;
    }
    jobTimer = setTimeout(tick, STATUS_POLL_MS);
  };
  tick();
}

/** Post-processing complete toast. Reports the system clipboard copy result and saved files. */
function notifyTranscriptReady(job) {
  const state = ctx.getState();
  const canOpen = state.mode === 'folder' && ctx.openFile;
  const lines = [];
  if (job.clipboardCopied) lines.push('Copied transcript.json to the clipboard. Paste it to use it.');
  if (job.clipboardError) lines.push(job.clipboardError);
  if (job.notificationError) lines.push(job.notificationError);
  if (job.promptRel || job.prompt) lines.push(`prompt.txt created: ${job.promptRel || job.prompt}`);
  if (job.transcript) lines.push(`Transcript: ${job.transcriptRel || job.transcript}`);
  if (job.note) lines.push(job.note);
  const text = lines.length ? lines.join('\n') : 'Post-processing done';
  if (job.clipboardError && job.transcript) {
    showToast(text, {
      action: { label: 'Copy transcript.json', run: () => copyTranscript(job.id) },
    });
  } else if (canOpen && job.promptRel) {
    showToast(text, {
      action: { label: 'Open prompt.txt', run: () => ctx.openFile(job.promptRel) },
    });
  } else if (job.prompt) {
    showToast(text, {
      action: { label: 'Open prompt.txt', run: () => window.open(recordingFileURL(job.id, 'prompt.txt'), '_blank', 'noopener') },
    });
  } else {
    showToast(text);
  }
}

function recordingFileURL(id, name) {
  return `${API_RECORDING_FILE_PATH}?id=${encodeURIComponent(id)}&name=${encodeURIComponent(name)}`;
}

async function copyTranscript(id) {
  try {
    const res = await fetch(recordingFileURL(id, 'transcript.json'));
    if (!res.ok) throw new Error('Unable to read the transcript.');
    await navigator.clipboard.writeText(await res.text());
    showToast('Copied transcript.json to the clipboard.');
  } catch {
    showToast('Could not copy to the clipboard. Please check the saved transcript.json file.', { error: true });
  }
}

// ---------------------------------------------------------------------------
// Event collection
// ---------------------------------------------------------------------------

function elapsedMs() {
  return Math.round(performance.now() - t0);
}

function pushEvent(event) {
  events.push({ t: elapsedMs(), ...event });
  updateEventCount();
}

/** Ctrl+C (⌘C) copy: logs the selection as source-based text/lines. */
function handleCopy() {
  if (!recording) return;
  const sel = window.getSelection();
  if (!sel || sel.isCollapsed || sel.rangeCount === 0) return;
  const state = ctx.getState();
  if (!state.file) return;

  const ranges = [];
  for (let i = 0; i < sel.rangeCount; i++) ranges.push(sel.getRangeAt(i));
  if (!ranges.some((r) => rangeIntersectsNode(r, ctx.preview))) return; // ignore copies outside the view

  const selectionText = sel.toString();
  const pdfPage = pdfPageOfRanges(ranges);
  if (pdfPage) {
    pushEvent({ type: 'copy', file: state.file, pdf: pdfRefOf(pdfPage), selection: selectionText });
    return;
  }
  const viewer = ctx.preview.querySelector('.code-viewer');
  if (viewer && ranges.some((r) => rangeIntersectsNode(r, viewer))) {
    copyFromCode(viewer, ranges, selectionText, state);
  } else {
    copyFromMarkdown(ranges, selectionText, state);
  }
}

/** If the selection overlaps a PDF text layer, returns the .pdf-page containing its start point. */
function pdfPageOfRanges(ranges) {
  for (const r of ranges) {
    const el = nodeElement(r.startContainer);
    const page = el && el.closest ? el.closest('.pdf-page') : null;
    if (page) return page;
  }
  return null;
}

/** Builds the pdf reference to log in an event from a .pdf-page element. */
function pdfRefOf(pageEl, point) {
  const ref = { page: Number(pageEl.dataset.pageNumber) || 1 };
  if (point) {
    ref.x = Math.round(point.x * PDF_COORD_DECIMALS) / PDF_COORD_DECIMALS;
    ref.y = Math.round(point.y * PDF_COORD_DECIMALS) / PDF_COORD_DECIMALS;
  }
  return ref;
}

/** Logs copy-button clicks on diagrams/code blocks as copy events. */
function handleManualCopy(e) {
  if (!recording) return;
  const state = ctx.getState();
  if (!state.file) return;
  const { text = '', el = null } = e.detail || {};
  const blockEl = el && el.closest ? el.closest('[data-block-index]') : null;
  if (blockEl) {
    recordBlockCopy(blockEl, text, state);
  } else {
    pushEvent({ type: 'copy', file: state.file, text, selection: text });
  }
}

function copyFromCode(viewer, ranges, selectionText, state) {
  const content = viewer.querySelector('.code-content');
  if (!content) return;
  const lineCount = Number(viewer.dataset.lineCount) || 1;
  let startLine = null;
  let endLine = null;
  for (const r of ranges) {
    if (!rangeIntersectsNode(r, content)) continue;
    const s = lineAtPoint(content, r.startContainer, r.startOffset, lineCount);
    let e = lineAtPoint(content, r.endContainer, r.endOffset, lineCount);
    // If the selection includes the trailing newline (triple-click, etc.), adjust so it does not spill onto the next line
    if (e > s && r.toString().endsWith('\n')) e -= 1;
    startLine = startLine === null ? s : Math.min(startLine, s);
    endLine = endLine === null ? e : Math.max(endLine, e);
  }
  if (startLine === null) return;
  const text = normalizedLines(state.raw).slice(startLine - 1, endLine).join('\n');
  pushEvent({
    type: 'copy',
    file: state.file,
    line: startLine,
    lineEnd: endLine,
    text,
    selection: selectionText,
  });
}

function copyFromMarkdown(ranges, selectionText, state) {
  let start = null;
  let end = null;
  ctx.preview.querySelectorAll('[data-block-index]').forEach((el) => {
    if (el.getAttribute('data-block-type') === 'empty') return;
    if (!ranges.some((r) => rangeIntersectsNode(r, el))) return;
    const block = (state.blocks || [])[Number(el.getAttribute('data-block-index'))];
    if (!block) return;
    if (start === null || block.startLine < start) start = block.startLine;
    if (end === null || block.endLine > end) end = block.endLine;
  });
  if (start === null) return;
  const mapped = mapLinesToRaw(state, start, end);
  pushEvent({
    type: 'copy',
    file: state.file,
    line: mapped.line,
    lineEnd: mapped.lineEnd,
    text: mapped.text,
    selection: selectionText,
    ...(mapped.approx ? { approx: true } : {}),
  });
}

function recordBlockCopy(blockEl, copiedText, state) {
  const block = (state.blocks || [])[Number(blockEl.getAttribute('data-block-index'))];
  if (!block) {
    pushEvent({ type: 'copy', file: state.file, text: copiedText, selection: copiedText });
    return;
  }
  const mapped = mapLinesToRaw(state, block.startLine, block.endLine);
  pushEvent({
    type: 'copy',
    file: state.file,
    line: mapped.line,
    lineEnd: mapped.lineEnd,
    text: mapped.text,
    selection: copiedText,
    ...(mapped.approx ? { approx: true } : {}),
  });
}

/** Highlights the clicked line/block as focused and logs it. */
function handleClick(e) {
  if (!recording) return;
  if (e.target.closest('.copy-button, .preview-media-copy')) return; // copy buttons are logged only as copy events
  const sel = window.getSelection();
  if (sel && !sel.isCollapsed) return; // a click that ends a drag selection is not treated as focus
  const state = ctx.getState();
  if (!state.file) return;

  const viewer = e.target.closest('.code-viewer');
  if (viewer) {
    focusCodeLine(viewer, e, state);
    return;
  }
  const pdfPage = e.target.closest('.pdf-page');
  if (pdfPage) {
    focusPdfPoint(pdfPage, e, state);
    return;
  }
  const blockEl = e.target.closest('[data-block-index]');
  if (blockEl) focusMarkdownBlock(blockEl, state);
}

/**
 * PDF page click: logs the page number and normalized coordinates (0–1) and marks the click point.
 * Includes the text around the click point so the location can be interpreted in the transcript.
 */
function focusPdfPoint(pageEl, e, state) {
  const rect = pageEl.getBoundingClientRect();
  if (!rect.width || !rect.height) return;
  const x = Math.min(Math.max((e.clientX - rect.left) / rect.width, 0), 1);
  const y = Math.min(Math.max((e.clientY - rect.top) / rect.height, 0), 1);
  markPdfPoint(pageEl, x, y);
  const nearby = nearestPdfText(pageEl, e.clientX, e.clientY);
  pushEvent({
    type: 'focus',
    file: state.file,
    pdf: pdfRefOf(pageEl, { x, y }),
    ...(nearby ? { text: nearby.slice(0, FOCUS_TEXT_LIMIT) } : {}),
  });
}

/** Finds the text of the text-layer span nearest the click point (within a radius; null if none). */
function nearestPdfText(pageEl, clientX, clientY) {
  let best = null;
  let bestDist = PDF_TEXT_SNAP_RADIUS_PX * PDF_TEXT_SNAP_RADIUS_PX;
  pageEl.querySelectorAll('.pdf-text-layer span').forEach((span) => {
    const text = (span.textContent || '').trim();
    if (!text) return;
    const r = span.getBoundingClientRect();
    if (!r.width && !r.height) return;
    const dx = Math.max(r.left - clientX, 0, clientX - r.right);
    const dy = Math.max(r.top - clientY, 0, clientY - r.bottom);
    const dist = dx * dx + dy * dy;
    if (dist <= bestDist) {
      bestDist = dist;
      best = text;
    }
  });
  return best;
}

function focusCodeLine(viewer, e, state) {
  const content = viewer.querySelector('.code-content');
  if (!content) return;
  const lineCount = Number(viewer.dataset.lineCount) || 1;
  const style = getComputedStyle(content);
  const lineHeight = parseFloat(style.lineHeight);
  const paddingTop = parseFloat(style.paddingTop) || 0;
  if (!lineHeight) return;

  const rect = content.getBoundingClientRect();
  let line = Math.floor((e.clientY - rect.top - paddingTop) / lineHeight) + 1;
  line = Math.min(Math.max(line, 1), lineCount);

  markCodeLine(viewer, content, line, lineHeight, paddingTop);
  const lineText = normalizedLines(state.raw)[line - 1] ?? '';
  pushEvent({
    type: 'focus',
    file: state.file,
    line,
    lineEnd: line,
    text: lineText.slice(0, FOCUS_TEXT_LIMIT),
  });
}

function focusMarkdownBlock(blockEl, state) {
  if (blockEl.getAttribute('data-block-type') === 'empty') return;
  clearFocusMarks();
  blockEl.classList.add('focus-block');
  const block = (state.blocks || [])[Number(blockEl.getAttribute('data-block-index'))];
  if (!block) return;
  const mapped = mapLinesToRaw(state, block.startLine, block.endLine);
  pushEvent({
    type: 'focus',
    file: state.file,
    line: mapped.line,
    lineEnd: mapped.lineEnd,
    text: mapped.text.slice(0, FOCUS_TEXT_LIMIT),
    ...(mapped.approx ? { approx: true } : {}),
  });
}

// ---------------------------------------------------------------------------
// Double-click word pick (word event)
// ---------------------------------------------------------------------------

/**
 * Double-click: logs the selected text as a word event. If the browser selected
 * nothing (e.g. blank space), finds and logs the first word after the click point,
 * and selects that word so it is visible what was logged.
 */
function handleDblClick(e) {
  if (!recording) return;
  if (e.target.closest('.copy-button, .preview-media-copy')) return;
  const state = ctx.getState();
  if (!state.file) return;

  const sel = window.getSelection();
  if (sel && !sel.isCollapsed && sel.rangeCount > 0) {
    const range = sel.getRangeAt(0);
    const el = nodeElement(range.startContainer);
    if (el && el.closest('.code-gutter')) return; // ignore double-clicks on the gutter (line number selection)
    if (rangeIntersectsNode(range, ctx.preview)) {
      const text = sel.toString().trim();
      if (text) {
        pushWordEvent(state, text, anchorLineRange(range.startContainer, range.startOffset, state));
        return;
      }
    }
  }
  const found = nextWordFromPoint(e.clientX, e.clientY);
  if (!found) return;
  selectFoundWord(found);
  pushWordEvent(state, found.word, anchorLineRange(found.node, found.start, state));
}

function pushWordEvent(state, text, range) {
  pushEvent({
    type: 'word',
    file: state.file,
    ...(range && range.pdf ? { pdf: pdfRefOf(range.pdf) } : {}),
    ...(range && range.line ? { line: range.line, lineEnd: range.lineEnd } : {}),
    text: text.slice(0, FOCUS_TEXT_LIMIT),
    ...(range && range.approx ? { approx: true } : {}),
  });
}

function nodeElement(node) {
  if (!node) return null;
  return node.nodeType === Node.TEXT_NODE ? node.parentElement : node;
}

/**
 * Gets the code line or Markdown block line range containing a text position (node, offset).
 * Inside a PDF text layer, returns the page element instead of lines.
 */
function anchorLineRange(node, offset, state) {
  const el = nodeElement(node);
  if (!el || !el.closest) return null;
  const pdfPage = el.closest('.pdf-page');
  if (pdfPage) return { pdf: pdfPage };
  const viewer = el.closest('.code-viewer');
  if (viewer) {
    const content = viewer.querySelector('.code-content');
    if (!content) return null;
    const lineCount = Number(viewer.dataset.lineCount) || 1;
    const line = lineAtPoint(content, node, offset, lineCount);
    return { line, lineEnd: line };
  }
  const blockEl = el.closest('[data-block-index]');
  if (!blockEl || blockEl.getAttribute('data-block-type') === 'empty') return null;
  const block = (state.blocks || [])[Number(blockEl.getAttribute('data-block-index'))];
  if (!block) return null;
  const mapped = mapLinesToRaw(state, block.startLine, block.endLine);
  return { line: mapped.line, lineEnd: mapped.lineEnd, approx: mapped.approx };
}

/** Gets the text position at screen coordinates using the browser-specific caret API. */
function caretFromPoint(x, y) {
  if (document.caretPositionFromPoint) {
    const pos = document.caretPositionFromPoint(x, y);
    return pos && pos.offsetNode ? { node: pos.offsetNode, offset: pos.offset } : null;
  }
  if (document.caretRangeFromPoint) {
    const r = document.caretRangeFromPoint(x, y);
    return r ? { node: r.startContainer, offset: r.startOffset } : null;
  }
  return null;
}

/** Finds the first word after the click point (in document order). null if none. */
function nextWordFromPoint(x, y) {
  const caret = caretFromPoint(x, y);
  if (!caret || !ctx.preview.contains(caret.node)) return null;
  const el = nodeElement(caret.node);
  if (!el || !el.closest) return null;
  let scope = ctx.preview;
  const viewer = el.closest('.code-viewer');
  if (viewer) {
    scope = viewer.querySelector('.code-content');
    if (!scope || !scope.contains(caret.node)) return null; // ignore areas outside the body, such as the gutter
  }
  const boundary = document.createRange();
  try {
    boundary.setStart(caret.node, caret.offset);
  } catch {
    return null;
  }
  boundary.collapse(true);
  const walker = document.createTreeWalker(scope, NodeFilter.SHOW_TEXT, {
    acceptNode(n) {
      if (!/\S/.test(n.nodeValue || '')) return NodeFilter.FILTER_REJECT;
      const p = n.parentElement;
      if (p && p.closest('.copy-button, .code-gutter')) return NodeFilter.FILTER_REJECT;
      return NodeFilter.FILTER_ACCEPT;
    },
  });
  let node;
  while ((node = walker.nextNode())) {
    let from = 0;
    if (node === caret.node) {
      from = caret.offset;
    } else if (boundary.comparePoint(node, node.nodeValue.length) <= 0) {
      continue; // text before the caret
    }
    const m = /\S+/.exec(node.nodeValue.slice(from));
    if (m) {
      const start = from + m.index;
      return { node, start, end: start + m[0].length, word: m[0] };
    }
  }
  return null;
}

/** Actually selects the logged word to show what was logged. */
function selectFoundWord(found) {
  try {
    const r = document.createRange();
    r.setStart(found.node, found.start);
    r.setEnd(found.node, found.end);
    const sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(r);
  } catch {
    /* a failure to show the selection does not affect logging */
  }
}

// ---------------------------------------------------------------------------
// Line mapping utilities
// ---------------------------------------------------------------------------

function normalizeNewlines(text) {
  return (text ?? '').replace(/\r\n?/g, '\n');
}

function normalizedLines(text) {
  return normalizeNewlines(text).split('\n');
}

/**
 * Maps a block-parser line range (in SmartMD-preprocessed text) to original file lines.
 * If preprocessing was an identity, returns it as is; otherwise searches the original for the same slice
 * and takes the closest match. If none is found, returns the preprocessed values marked approx.
 */
function mapLinesToRaw(state, startLine, endLine) {
  const raw = normalizeNewlines(state.raw);
  const transformed = normalizeNewlines(state.transformed ?? state.raw);
  const slice = transformed.split('\n').slice(startLine - 1, endLine).join('\n');
  if (transformed === raw) {
    return { line: startLine, lineEnd: endLine, text: slice, approx: false };
  }
  const found = findClosestLine(raw, slice, startLine);
  if (found !== null) {
    return { line: found, lineEnd: found + (endLine - startLine), text: slice, approx: false };
  }
  return { line: startLine, lineEnd: endLine, text: slice, approx: true };
}

/** Returns the line in raw where slice appears that is closest to hintLine. */
function findClosestLine(raw, slice, hintLine) {
  if (!slice) return null;
  let best = null;
  let idx = raw.indexOf(slice);
  while (idx !== -1) {
    const line = raw.slice(0, idx).split('\n').length;
    if (best === null || Math.abs(line - hintLine) < Math.abs(best - hintLine)) best = line;
    idx = raw.indexOf(slice, idx + 1);
  }
  return best;
}

/** Computes a line number from the text between the start of content and (container, offset). */
function lineAtPoint(content, container, offset, lineCount) {
  if (!content.contains(container)) {
    // If the selection endpoint is outside the code area (e.g. gutter), clamp it by checking before/after in document order.
    const pos = content.compareDocumentPosition(container);
    if (pos & Node.DOCUMENT_POSITION_PRECEDING) return 1;
    return lineCount;
  }
  const probe = document.createRange();
  probe.selectNodeContents(content);
  try {
    probe.setEnd(container, offset);
  } catch {
    return lineCount;
  }
  const line = probe.toString().split('\n').length;
  return Math.min(line, lineCount);
}

function rangeIntersectsNode(range, node) {
  try {
    return range.intersectsNode(node);
  } catch {
    return false;
  }
}

// ---------------------------------------------------------------------------
// Focus indicator (highlight)
// ---------------------------------------------------------------------------

function markCodeLine(viewer, content, line, lineHeight, paddingTop) {
  clearFocusMarks();
  const bar = document.createElement('div');
  bar.className = 'code-focus-line';
  bar.style.top = `${content.offsetTop + paddingTop + (line - 1) * lineHeight}px`;
  bar.style.height = `${lineHeight}px`;
  bar.style.width = `${viewer.scrollWidth}px`;
  viewer.appendChild(bar);
}

function markPdfPoint(pageEl, x, y) {
  clearFocusMarks();
  const dot = document.createElement('div');
  dot.className = 'pdf-focus-point';
  dot.style.left = `${x * 100}%`;
  dot.style.top = `${y * 100}%`;
  pageEl.appendChild(dot);
}

function clearFocusMarks() {
  ctx.preview.querySelectorAll('.code-focus-line').forEach((el) => el.remove());
  ctx.preview.querySelectorAll('.focus-block').forEach((el) => el.classList.remove('focus-block'));
  ctx.preview.querySelectorAll('.pdf-focus-point').forEach((el) => el.remove());
}

// ---------------------------------------------------------------------------
// UI (button/timer/toast)
// ---------------------------------------------------------------------------

function setRecordingUI(on) {
  const title = on ? 'Stop and save recording' : 'Start audio recording';
  ui.button.classList.toggle('recording', on);
  ui.button.title = title;
  ui.button.setAttribute('aria-label', title);
  ui.button.setAttribute('aria-pressed', String(on));
  ui.status.hidden = !on;
  if (ui.deviceSelect) ui.deviceSelect.disabled = on; // prevent device changes while recording
  if (on) {
    updateTimer();
    updateEventCount();
    timerId = setInterval(updateTimer, TIMER_INTERVAL_MS);
  } else if (timerId) {
    clearInterval(timerId);
    timerId = null;
  }
}

function updateTimer() {
  ui.timer.textContent = formatClock(elapsedMs());
}

function updateEventCount() {
  if (!recording) return;
  ui.eventsCount.textContent = `Events ${events.length}`;
}

function formatClock(ms) {
  const total = Math.floor(ms / 1000);
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const mmss = `${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}`;
  return h > 0 ? `${h}:${mmss}` : mmss;
}

function handleBeforeUnload(e) {
  if (!recording) return;
  e.preventDefault();
  e.returnValue = ''; // show the browser's default confirmation dialog
}

export function showToast(message, { error = false, download = false, action = null } = {}) {
  if (!toastEl) {
    toastEl = document.createElement('div');
    toastEl.id = 'record-toast';
    document.body.appendChild(toastEl);
  }
  toastEl.className = error ? 'error' : '';
  toastEl.replaceChildren();

  const text = document.createElement('span');
  text.textContent = message;
  toastEl.appendChild(text);

  if (download) {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.textContent = 'Download as file';
    btn.addEventListener('click', () => {
      downloadFallback();
      hideToast();
    });
    toastEl.appendChild(btn);
  }

  if (action) {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.textContent = action.label;
    btn.addEventListener('click', () => {
      hideToast();
      action.run();
    });
    toastEl.appendChild(btn);
  }

  const close = document.createElement('button');
  close.type = 'button';
  close.className = 'toast-close';
  close.textContent = '✕';
  close.setAttribute('aria-label', 'Close');
  close.addEventListener('click', hideToast);
  toastEl.appendChild(close);

  toastEl.hidden = false;
  if (toastTimer) {
    clearTimeout(toastTimer);
    toastTimer = null;
  }
  if (!error) toastTimer = setTimeout(hideToast, TOAST_HIDE_DELAY_MS);
}

function hideToast() {
  if (toastEl) toastEl.hidden = true;
  if (toastTimer) {
    clearTimeout(toastTimer);
    toastTimer = null;
  }
}

/** On upload failure, preserves the audio and event JSON via browser download. */
function downloadFallback() {
  if (!lastFailed) return;
  const stamp = startedAt.replace(/[:.]/g, '-');
  const baseType = (lastFailed.blob.type || '').split(';')[0];
  const ext = DOWNLOAD_EXT_BY_MIME[baseType] || 'webm';
  triggerDownload(lastFailed.blob, `mdview-rec-${stamp}.${ext}`);
  triggerDownload(
    new Blob([lastFailed.metaJson], { type: 'application/json' }),
    `mdview-rec-${stamp}.events.json`
  );
}

function triggerDownload(blob, filename) {
  const a = document.createElement('a');
  a.href = URL.createObjectURL(blob);
  a.download = filename;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(a.href), OBJECT_URL_REVOKE_DELAY_MS);
}
