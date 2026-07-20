// recorder.js — 오디오 녹화 + 문서 참조 이벤트 타임라인 기록 Worker
//
// 툴바의 녹화 버튼으로 마이크 녹음을 시작/종료하고, 녹화 중 사용자의
//   - 파일 열람(open)
//   - 텍스트 복사(copy: Ctrl+C / 복사 버튼)
//   - 라인/블록 클릭(focus)
//   - 단어 지정(word: 더블클릭 — 선택된 텍스트, 없으면 클릭 위치 뒤의 첫 단어)
// 을 오디오 시간축(t: 녹음 시작 기준 ms)에 맞춰 기록한다.
// 종료 시 오디오와 이벤트 메타(JSON)를 서버(/api/recording)에 업로드해
// mdview-recordings/rec-<timestamp>/ 아래 저장한다.
//
// 라인 번호 정책:
//   - 코드뷰어: 파일의 실제 라인 번호 (클릭 좌표/선택 영역에서 계산)
//   - 마크다운: 블록 단위 라인 범위. SmartMD 전처리로 라인이 어긋난 경우
//     원본 텍스트에서 동일 슬라이스를 탐색해 보정하고, 실패하면 approx로 표시한다.

const API_RECORDING_PATH = '/api/recording';
const API_RECORDING_STATUS_PATH = '/api/recording/status';
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

// 브라우저별 지원 우선순위 (Chrome/Edge: webm+opus, Safari: mp4)
const MIME_CANDIDATES = [
  'audio/webm;codecs=opus',
  'audio/webm',
  'audio/mp4',
  'audio/ogg;codecs=opus',
];

const DOWNLOAD_EXT_BY_MIME = { 'audio/webm': 'webm', 'audio/mp4': 'm4a', 'audio/ogg': 'ogg' };

let ctx = null; // app.js가 주입: { preview, getState() → {mode, root, file, raw, transformed, blocks} }
let ui = null;

let recording = false;
let mediaRecorder = null;
let mediaStream = null;
let chunks = [];
let events = [];
let t0 = 0;
let startedAt = '';
let timerId = null;
let lastFailed = null; // 업로드 실패 시 {blob, metaJson} 보관 (다운로드 폴백용)

let toastEl = null;
let toastTimer = null;
let jobTimer = null;         // 전사 상태 폴링 타이머
let activeDeviceLabel = '';  // 현재 녹음에 사용된 입력 장치 이름

/**
 * 녹화 기능을 초기화한다.
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

  // 오디오 입력 장치 선택
  if (ui.deviceSelect) {
    ui.deviceSelect.addEventListener('change', () => {
      try {
        localStorage.setItem(DEVICE_STORE_KEY, ui.deviceSelect.value);
      } catch {
        /* localStorage 불가 환경 무시 */
      }
    });
    populateDevices();
    if (navigator.mediaDevices && navigator.mediaDevices.addEventListener) {
      navigator.mediaDevices.addEventListener('devicechange', populateDevices);
    }
  }
}

// ---------------------------------------------------------------------------
// 오디오 입력 장치 선택
// ---------------------------------------------------------------------------

function savedDeviceId() {
  try {
    return localStorage.getItem(DEVICE_STORE_KEY) || '';
  } catch {
    return '';
  }
}

/**
 * 입력 장치 목록으로 드롭다운을 갱신한다.
 * 마이크 권한 허용 전에는 브라우저가 label을 비워 주므로 "마이크 N"으로 표시되고,
 * 첫 녹음(권한 허용) 이후 다시 호출되어 실제 장치 이름으로 채워진다.
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
  def.textContent = '기본 마이크';
  ui.deviceSelect.appendChild(def);

  let index = 0;
  let savedFound = saved === '';
  for (const d of devices) {
    if (d.kind !== 'audioinput') continue;
    if (!d.deviceId || d.deviceId === 'default' || d.deviceId === 'communications') continue;
    index += 1;
    const opt = document.createElement('option');
    opt.value = d.deviceId;
    opt.textContent = d.label || `마이크 ${index}`;
    if (d.deviceId === saved) {
      opt.selected = true;
      savedFound = true;
    }
    ui.deviceSelect.appendChild(opt);
  }
  if (!savedFound) ui.deviceSelect.value = '';
}

/** 파일이 열릴 때 호출된다 (녹화 중이면 open 이벤트로 기록). */
export function noteFileOpen(file) {
  if (!recording || !file) return;
  pushEvent({ type: 'open', file });
}

function toggleRecording() {
  if (recording) stopRecording();
  else startRecording();
}

// ---------------------------------------------------------------------------
// 마이크 권한 진단
// ---------------------------------------------------------------------------

const MIC_DENIED_GUIDE =
  '이 사이트의 마이크 권한이 차단되어 있습니다.\n' +
  '· 주소창의 사이트 권한 아이콘(자물쇠/설정)에서 마이크를 "허용"으로 변경 후 새로고침\n' +
  '· macOS: 시스템 설정 → 개인정보 보호 및 보안 → 마이크에서 사용 중인 브라우저를 켠 뒤 브라우저 재시작';

/** Permissions API로 마이크가 이미 차단 상태인지 확인한다 (미지원 브라우저는 false). */
async function micPermissionDenied() {
  if (!navigator.permissions || !navigator.permissions.query) return false;
  try {
    const status = await navigator.permissions.query({ name: 'microphone' });
    return status.state === 'denied';
  } catch {
    return false; // Safari 등 'microphone' 미지원 → getUserMedia에서 판별
  }
}

/** getUserMedia 실패 원인별 안내 메시지를 만든다. */
function micErrorMessage(err) {
  switch (err.name) {
    case 'NotAllowedError':
    case 'PermissionDeniedError':
      return MIC_DENIED_GUIDE;
    case 'NotFoundError':
    case 'DevicesNotFoundError':
      return '사용 가능한 마이크 장치를 찾을 수 없습니다. 입력 장치 연결을 확인하세요.';
    case 'NotReadableError':
    case 'TrackStartError':
      return '다른 앱이 마이크를 사용 중이라 접근할 수 없습니다. 해당 앱을 종료 후 다시 시도하세요.';
    default:
      return `마이크를 열 수 없습니다: ${err.name} — ${err.message}`;
  }
}

async function startRecording() {
  if (!navigator.mediaDevices || !window.MediaRecorder) {
    showToast(
      '이 브라우저는 오디오 녹음을 지원하지 않습니다.\n' +
        `(secure context 필요 — 주소가 localhost인지 확인하세요: ${location.origin})`,
      { error: true }
    );
    return;
  }
  // 이미 "차단" 상태면 getUserMedia를 불러도 프롬프트 없이 실패하므로 먼저 안내한다.
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
    // 선택한 장치가 뽑혔거나 사라진 경우: 기본 마이크로 폴백
    if (deviceId && (err.name === 'OverconstrainedError' || err.name === 'NotFoundError')) {
      try {
        stream = await navigator.mediaDevices.getUserMedia({ audio: true });
        showToast('선택한 마이크를 찾을 수 없어 기본 마이크로 녹음합니다.');
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
  // 권한 허용 후에는 장치 label이 채워지므로 목록을 갱신한다.
  populateDevices();
  const track = stream.getAudioTracks()[0];
  activeDeviceLabel = track ? track.label : '';
  const mimeType = MIME_CANDIDATES.find((m) => MediaRecorder.isTypeSupported(m));
  try {
    mediaRecorder = new MediaRecorder(stream, mimeType ? { mimeType } : undefined);
  } catch (err) {
    stream.getTracks().forEach((t) => t.stop());
    showToast(`녹음을 시작할 수 없습니다: ${err.message}`, { error: true });
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
    showToast(`녹음 중 오류가 발생했습니다: ${e.error ? e.error.message : e.type}`, { error: true });
  });
  mediaRecorder.start(AUDIO_TIMESLICE_MS);

  startLevelMeter(stream);
  hideToast();
  setRecordingUI(true);
  const state = ctx.getState();
  if (state.file) pushEvent({ type: 'open', file: state.file });
}

// ---------------------------------------------------------------------------
// 입력 레벨 미터 — 마이크가 실제로 소리를 받고 있는지 시각 확인용
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
    /* 미터 실패는 녹음 자체에 영향 없음 */
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
  mediaRecorder.stop(); // → 'stop' 이벤트에서 finalizeRecording 실행
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
      '녹음된 오디오 데이터가 없습니다. 녹음 중 상단의 입력 레벨이 움직이는지 확인하세요.\n' +
        '(macOS: 시스템 설정 → 개인정보 보호 및 보안 → 마이크에서 브라우저 허용 여부 확인)',
      { error: true }
    );
    return;
  }
  uploadRecording(blob, buildMeta(durationMs));
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
  setJobStatus('업로드 중', { spin: true });
  try {
    const res = await fetch(API_RECORDING_PATH, { method: 'POST', body: form });
    if (!res.ok) {
      const detail = (await res.text()).slice(0, ERROR_TEXT_LIMIT);
      throw new Error(detail || res.statusText);
    }
    const result = await res.json();
    lastFailed = null;
    if (result.transcribing) {
      setJobStatus('전사 중', { spin: true });
      pollTranscribeStatus(result.id);
      showToast(
        `녹화 저장 완료 (이벤트 ${meta.events.length}개)\n${result.dir}\n` +
          '음성→텍스트 전사가 백그라운드에서 진행 중입니다 (완료 시 알림)'
      );
    } else {
      setJobStatus('');
      showToast(
        `녹화 저장 완료 (이벤트 ${meta.events.length}개)\n${result.dir}\n` +
          `전사(음성→텍스트): mdview transcribe "${result.dir}"`
      );
    }
  } catch (err) {
    setJobStatus('');
    lastFailed = { blob, metaJson };
    showToast(`녹화 업로드에 실패했습니다: ${err.message}`, { error: true, download: true });
  }
}

// ---------------------------------------------------------------------------
// 전사 진행 상태 (툴바 칩 + 서버 폴링)
// ---------------------------------------------------------------------------

/** 툴바의 작업 상태 칩을 갱신한다. 빈 텍스트면 숨긴다. */
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

/** 자동 전사 상태를 주기적으로 조회해 완료/실패를 알린다. */
function pollTranscribeStatus(id) {
  const startedAtMs = Date.now();
  const tick = async () => {
    let job = null;
    try {
      const res = await fetch(`${API_RECORDING_STATUS_PATH}?id=${encodeURIComponent(id)}`);
      if (res.ok) job = await res.json();
    } catch {
      /* 일시적 네트워크 오류 — 다음 폴링에서 재시도 */
    }
    if (job && job.state === 'done') {
      setJobStatus('전사 완료', { done: true });
      setTimeout(() => setJobStatus(''), JOB_DONE_HIDE_MS);
      notifyTranscriptReady(job);
      return;
    }
    if (job && job.state === 'error') {
      setJobStatus('');
      showToast(`자동 전사 실패: ${(job.error || '').slice(0, ERROR_TEXT_LIMIT)}`, { error: true });
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

function notifyTranscriptReady(job) {
  const state = ctx.getState();
  if (state.mode === 'folder' && job.transcriptRel && ctx.openFile) {
    showToast(`전사 완료: ${job.transcriptRel}`, {
      action: { label: '열기', run: () => ctx.openFile(job.transcriptRel) },
    });
  } else {
    showToast(`전사 완료: ${job.transcript || ''}`);
  }
}

// ---------------------------------------------------------------------------
// 이벤트 수집
// ---------------------------------------------------------------------------

function elapsedMs() {
  return Math.round(performance.now() - t0);
}

function pushEvent(event) {
  events.push({ t: elapsedMs(), ...event });
  updateEventCount();
}

/** Ctrl+C(⌘C) 복사: 선택 영역을 소스 기준 텍스트/라인으로 기록한다. */
function handleCopy() {
  if (!recording) return;
  const sel = window.getSelection();
  if (!sel || sel.isCollapsed || sel.rangeCount === 0) return;
  const state = ctx.getState();
  if (!state.file) return;

  const ranges = [];
  for (let i = 0; i < sel.rangeCount; i++) ranges.push(sel.getRangeAt(i));
  if (!ranges.some((r) => rangeIntersectsNode(r, ctx.preview))) return; // 뷰 밖 복사는 무시

  const selectionText = sel.toString();
  const viewer = ctx.preview.querySelector('.code-viewer');
  if (viewer && ranges.some((r) => rangeIntersectsNode(r, viewer))) {
    copyFromCode(viewer, ranges, selectionText, state);
  } else {
    copyFromMarkdown(ranges, selectionText, state);
  }
}

/** 다이어그램/코드 블록의 복사 버튼 클릭을 copy 이벤트로 기록한다. */
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
    // 선택이 줄 끝 개행까지 포함하면(트리플클릭 등) 다음 줄로 넘어가지 않게 보정
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

/** 클릭한 라인/블록을 포커스로 표시하고 기록한다. */
function handleClick(e) {
  if (!recording) return;
  if (e.target.closest('.copy-button')) return; // 복사 버튼은 copy 이벤트로만 기록
  const sel = window.getSelection();
  if (sel && !sel.isCollapsed) return; // 드래그 선택을 마친 클릭은 포커스로 취급하지 않음
  const state = ctx.getState();
  if (!state.file) return;

  const viewer = e.target.closest('.code-viewer');
  if (viewer) {
    focusCodeLine(viewer, e, state);
    return;
  }
  const blockEl = e.target.closest('[data-block-index]');
  if (blockEl) focusMarkdownBlock(blockEl, state);
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
// 더블클릭 단어 지정 (word 이벤트)
// ---------------------------------------------------------------------------

/**
 * 더블클릭: 선택된 텍스트를 word 이벤트로 기록한다. 브라우저가 아무것도
 * 선택하지 않았다면(여백 등) 클릭 위치 뒤의 첫 단어를 찾아 기록하고,
 * 기록된 단어가 무엇인지 보이도록 그 단어를 선택 상태로 만든다.
 */
function handleDblClick(e) {
  if (!recording) return;
  if (e.target.closest('.copy-button')) return;
  const state = ctx.getState();
  if (!state.file) return;

  const sel = window.getSelection();
  if (sel && !sel.isCollapsed && sel.rangeCount > 0) {
    const range = sel.getRangeAt(0);
    const el = nodeElement(range.startContainer);
    if (el && el.closest('.code-gutter')) return; // 거터 더블클릭(라인 번호 선택)은 무시
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
    ...(range ? { line: range.line, lineEnd: range.lineEnd } : {}),
    text: text.slice(0, FOCUS_TEXT_LIMIT),
    ...(range && range.approx ? { approx: true } : {}),
  });
}

function nodeElement(node) {
  if (!node) return null;
  return node.nodeType === Node.TEXT_NODE ? node.parentElement : node;
}

/** 텍스트 위치(node, offset)가 속한 코드 라인 또는 마크다운 블록의 라인 범위를 구한다. */
function anchorLineRange(node, offset, state) {
  const el = nodeElement(node);
  if (!el || !el.closest) return null;
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

/** 브라우저별 caret API로 화면 좌표에 해당하는 텍스트 위치를 얻는다. */
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

/** 클릭 지점 뒤(문서 순서상)의 첫 단어를 찾는다. 없으면 null. */
function nextWordFromPoint(x, y) {
  const caret = caretFromPoint(x, y);
  if (!caret || !ctx.preview.contains(caret.node)) return null;
  const el = nodeElement(caret.node);
  if (!el || !el.closest) return null;
  let scope = ctx.preview;
  const viewer = el.closest('.code-viewer');
  if (viewer) {
    scope = viewer.querySelector('.code-content');
    if (!scope || !scope.contains(caret.node)) return null; // 거터 등 본문 밖은 무시
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
      continue; // 캐럿보다 앞의 텍스트
    }
    const m = /\S+/.exec(node.nodeValue.slice(from));
    if (m) {
      const start = from + m.index;
      return { node, start, end: start + m[0].length, word: m[0] };
    }
  }
  return null;
}

/** 기록된 단어를 실제 선택 상태로 만들어 무엇이 기록됐는지 보여 준다. */
function selectFoundWord(found) {
  try {
    const r = document.createRange();
    r.setStart(found.node, found.start);
    r.setEnd(found.node, found.end);
    const sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(r);
  } catch {
    /* 선택 표시 실패는 기록에 영향 없음 */
  }
}

// ---------------------------------------------------------------------------
// 라인 매핑 유틸
// ---------------------------------------------------------------------------

function normalizeNewlines(text) {
  return (text ?? '').replace(/\r\n?/g, '\n');
}

function normalizedLines(text) {
  return normalizeNewlines(text).split('\n');
}

/**
 * 블록 파서 기준 라인 범위(SmartMD 전처리 텍스트)를 원본 파일 라인으로 보정한다.
 * 전처리가 항등이면 그대로, 아니면 원본에서 동일 슬라이스를 탐색해 가장 가까운
 * 위치를 채택한다. 찾지 못하면 전처리 기준 값을 approx로 반환한다.
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

/** raw 안에서 slice가 나타나는 라인 중 hintLine에 가장 가까운 것을 반환한다. */
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

/** content 시작부터 (container, offset)까지의 텍스트로 라인 번호를 계산한다. */
function lineAtPoint(content, container, offset, lineCount) {
  if (!content.contains(container)) {
    // 선택 끝점이 코드 영역 밖(거터 등)이면 문서 순서로 앞/뒤를 판별해 클램프한다.
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
// 포커스 표시 (하이라이트)
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

function clearFocusMarks() {
  ctx.preview.querySelectorAll('.code-focus-line').forEach((el) => el.remove());
  ctx.preview.querySelectorAll('.focus-block').forEach((el) => el.classList.remove('focus-block'));
}

// ---------------------------------------------------------------------------
// UI (버튼/타이머/토스트)
// ---------------------------------------------------------------------------

function setRecordingUI(on) {
  const title = on ? '녹화 중지 및 저장' : '오디오 녹화 시작';
  ui.button.classList.toggle('recording', on);
  ui.button.title = title;
  ui.button.setAttribute('aria-label', title);
  ui.button.setAttribute('aria-pressed', String(on));
  ui.status.hidden = !on;
  if (ui.deviceSelect) ui.deviceSelect.disabled = on; // 녹화 중 장치 변경 방지
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
  if (recording) ui.eventsCount.textContent = `이벤트 ${events.length}`;
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
  e.returnValue = ''; // 브라우저 기본 확인 대화상자 표시
}

function showToast(message, { error = false, download = false, action = null } = {}) {
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
    btn.textContent = '파일로 다운로드';
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
  close.setAttribute('aria-label', '닫기');
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

/** 업로드 실패 시 오디오와 이벤트 JSON을 브라우저 다운로드로 보존한다. */
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
