// webopen.js — 툴바 「웹 열기」 Worker
//
// 입력한 URL을 서버(/api/web/open)에 보내 전용 Chrome 창에서 열게 한다.
// 녹음 중이면 recorder.js에 web-open 이벤트를 남긴다. 마지막 URL은 브라우저에 기억한다.

import { normalizeWebURL } from './web-url.js';
import { isRecording, noteWebOpen, showToast } from './recorder.js';

const API_WEB_OPEN_PATH = '/api/web/open';
const URL_STORE_KEY = 'mdview-web-url';
const ERROR_TEXT_LIMIT = 200;

let input = null;
let button = null;

export function initWebOpen() {
  input = document.getElementById('web-url');
  button = document.getElementById('web-open');
  if (!input || !button) return;

  try {
    const saved = localStorage.getItem(URL_STORE_KEY);
    if (saved) input.value = saved;
  } catch {
    /* localStorage 불가 환경 무시 */
  }

  button.addEventListener('click', openFromInput);
  input.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') {
      e.preventDefault();
      openFromInput();
    }
  });
}

async function openFromInput() {
  if (button.disabled) return;
  const url = normalizeWebURL(input.value);
  if (!url) {
    showToast('http/https 주소를 입력하세요 (예: https://example.com)', { error: true });
    input.focus();
    return;
  }
  button.disabled = true;
  try {
    const res = await fetch(API_WEB_OPEN_PATH, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ url }),
    });
    if (!res.ok) {
      const detail = (await res.text()).slice(0, ERROR_TEXT_LIMIT);
      throw new Error(detail || res.statusText);
    }
    const result = await res.json();
    // 서버가 검증·정규화한 주소를 기준으로 기록·저장한다 (구버전 서버면 입력값으로 대체).
    const opened = result.url || url;
    try {
      localStorage.setItem(URL_STORE_KEY, opened);
    } catch {
      /* 무시 */
    }
    noteWebOpen(opened);
    showToast(result.launched
      ? `전용 Chrome 창을 열었습니다: ${opened}\n녹음 중 그 창에서의 클릭·더블클릭·복사가 함께 기록됩니다.`
      : `새 탭으로 열었습니다: ${opened}${isRecording() ? '\n녹음 중 그 창에서의 클릭·더블클릭·복사가 함께 기록됩니다.' : ''}`);
  } catch (err) {
    showToast(`웹 페이지를 열지 못했습니다: ${err.message}`, { error: true });
  } finally {
    button.disabled = false;
  }
}
