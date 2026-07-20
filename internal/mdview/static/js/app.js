import { renderMarkdown } from './markdown-renderer.js';
import { renderCode, isMarkdownPath, isCodeViewerPath } from './code-viewer.js';
import { initSidebar, highlightFile, hasFile } from './sidebar.js';
import { initSidebarResizer } from './sidebar-resizer.js';
import { initRecorder, noteFileOpen } from './recorder.js';

const WS_PROTOCOL = 'ws:';
const API_MODE_PATH = '/api/mode';
const API_FILE_PATH = '/api/file';
const API_SELECT_PATH = '/api/select';
const RECONNECT_DELAY_MS = 1000;

const preview = document.getElementById('preview');
const filenameDisplay = document.getElementById('filename-display');
const sidebar = document.getElementById('sidebar');
const emptyState = document.getElementById('empty-state');

const currentBlocksRef = { current: [], transformed: '' };

// 녹화 이벤트 기록에 사용하는 현재 파일 원본 텍스트
const contentRef = { raw: '' };

// 현재 폴더 모드에서 사용자가 선택한 상대 경로 (슬래시 구분)
let activeRelPath = null;

// 렌더링 방식 결정에 사용하는 현재 파일 이름 (folder 모드: 상대 경로, file 모드: 절대 경로)
let activeFileName = null;

let folderMode = false;

// /api/mode 응답 (녹화 메타에 root/file 정보로 사용)
let modeInfo = null;

// 연속 클릭 시 늦게 도착한 이전 요청의 응답을 폐기하기 위한 시퀀스
let selectSeq = 0;

function getWebSocketURL() {
  const port = window.location.port;
  const host = window.location.hostname;
  return `${WS_PROTOCOL}//${host}:${port}/ws`;
}

function setEmptyState(visible) {
  emptyState.hidden = !visible;
  preview.hidden = visible;
}

// 현재 파일 타입에 따라 마크다운 렌더링 또는 코드뷰어로 표시한다.
// folder 모드는 서버 화이트리스트를 신뢰해 비-md 는 모두 코드뷰어로,
// file 모드는 알려진 코드/설정 타입만 코드뷰어로 보내고 나머지는 기존처럼 마크다운으로 렌더링한다.
function renderContent(text) {
  setEmptyState(false);
  contentRef.raw = text;
  if (activeFileName && useCodeViewer(activeFileName)) {
    currentBlocksRef.current = [];
    currentBlocksRef.transformed = '';
    renderCode(text, activeFileName, preview);
    return;
  }
  renderMarkdown(text, preview, currentBlocksRef);
}

function useCodeViewer(fileName) {
  if (isMarkdownPath(fileName)) return false;
  return folderMode || isCodeViewerPath(fileName);
}

function setFilenameDisplay(name) {
  filenameDisplay.textContent = name;
  document.title = name || 'Markdown Viewer';
}

async function loadMode() {
  const res = await fetch(API_MODE_PATH);
  return res.json();
}

/**
 * 폴더 모드 전용: 상대 경로의 파일을 열고 뷰를 갱신한다.
 * @param {string} relPath  루트 기준 상대 경로 (슬래시 구분)
 * @param {{pushHistory?: boolean, hash?: string}} [options]
 */
async function selectFile(relPath, options = {}) {
  const { pushHistory = true, hash = '' } = options;
  const seq = ++selectSeq;
  activeRelPath = relPath;
  activeFileName = relPath;
  setFilenameDisplay(relPath);
  highlightFile(relPath);

  // URL 바 갱신 (뒤로가기 지원 및 공유 가능한 딥링크)
  if (pushHistory) {
    const url = '/' + relPath.split('/').map(encodeURIComponent).join('/') + (hash ? '#' + hash : '');
    history.pushState({ path: relPath }, '', url);
  }

  // 서버에 활성 파일 지정 (WS 브로드캐스트 대상)
  await fetch(`${API_SELECT_PATH}?path=${encodeURIComponent(relPath)}`);
  const res = await fetch(`${API_FILE_PATH}?path=${encodeURIComponent(relPath)}`);
  if (seq !== selectSeq) return;
  if (!res.ok) {
    const message = document.createElement('p');
    message.style.color = '#dc3545';
    message.textContent = `파일을 불러오지 못했습니다: ${relPath}`;
    preview.replaceChildren(message);
    setEmptyState(false);
    return;
  }
  const text = await res.text();
  if (seq !== selectSeq) return;
  renderContent(text);
  noteFileOpen(relPath);

  if (hash) {
    // 렌더링 직후 앵커로 스크롤 (마크다운 렌더러가 heading id를 생성함)
    requestAnimationFrame(() => {
      const target = document.getElementById(hash) || document.querySelector(`[name="${CSS.escape(hash)}"]`);
      if (target) target.scrollIntoView({ behavior: 'smooth', block: 'start' });
    });
  }
}

/**
 * preview 내 링크 클릭을 가로채 폴더 모드에서의 문서 간 이동을 구현한다.
 * 외부 링크·mailto·단순 해시는 기본 동작을 유지한다.
 */
function installLinkInterceptor() {
  preview.addEventListener('click', (e) => {
    const anchor = e.target.closest('a');
    if (!anchor) return;
    const href = anchor.getAttribute('href');
    if (!href) return;

    // 외부 스킴 / 프로토콜 상대 URL / mailto 등은 그대로 둔다.
    if (/^([a-z][a-z0-9+.-]*:)?\/\//i.test(href)) return;
    if (/^[a-z][a-z0-9+.-]*:/i.test(href) && !href.startsWith('http:') && !href.startsWith('https:')) return;

    // 순수 해시 링크는 기본 동작(동일 페이지 내 스크롤) 유지
    if (href.startsWith('#')) return;

    const [pathPart, hashPart = ''] = href.split('#');
    if (!pathPart) return;

    // 현재 활성 파일 디렉토리 기준으로 상대 경로 해석
    const baseUrl = new URL(
      '/' + (activeRelPath || ''),
      window.location.origin
    );
    const resolved = new URL(pathPart, baseUrl);
    const relPath = decodeURIComponent(resolved.pathname.replace(/^\/+/, ''));
    if (!relPath) return;

    // .md 링크와 트리에 존재하는 파일 링크만 인터셉트한다.
    if (!isMarkdownPath(relPath) && !hasFile(relPath)) return;

    e.preventDefault();
    selectFile(relPath, { hash: hashPart });
  });
}

// 서버가 뷰어 대상 파일 경로에만 SPA를 서빙하므로 여기서는 경로 존재 여부만 본다.
function pathFromLocation() {
  const p = decodeURIComponent(window.location.pathname || '');
  if (p === '' || p === '/') return null;
  return p.replace(/^\/+/, '') || null;
}

function installHistoryNav() {
  window.addEventListener('popstate', () => {
    const p = pathFromLocation();
    if (p) {
      selectFile(p, { pushHistory: false, hash: (window.location.hash || '').replace(/^#/, '') });
    }
  });
}

function connectWebSocket(mode) {
  const ws = new WebSocket(getWebSocketURL());

  ws.onmessage = (event) => {
    if (mode === 'file') {
      renderContent(event.data);
      return;
    }
    try {
      const data = JSON.parse(event.data);
      if (data.path && data.path === activeRelPath) {
        renderContent(data.content);
      }
    } catch {
      renderContent(event.data);
    }
  };

  ws.onclose = () => {
    setTimeout(() => connectWebSocket(mode), RECONNECT_DELAY_MS);
  };
}

async function bootstrap() {
  window.mermaid.initialize({ startOnLoad: false, theme: 'default' });

  const info = await loadMode();
  modeInfo = info;

  // 녹화 Worker 초기화: 현재 뷰 상태를 스냅샷으로 제공한다.
  initRecorder({
    preview,
    getState: () => ({
      mode: modeInfo ? modeInfo.mode : 'file',
      root: modeInfo ? modeInfo.root : '',
      file: activeFileName,
      raw: contentRef.raw,
      transformed: currentBlocksRef.transformed,
      blocks: currentBlocksRef.current,
    }),
    // 전사 완료 토스트의 "열기" 액션 (folder 모드 전용)
    openFile: (relPath) => {
      if (folderMode) selectFile(relPath);
    },
  });

  if (info.mode === 'file') {
    activeFileName = info.filename;
    setFilenameDisplay(info.filename);
    setEmptyState(false);
    connectWebSocket('file');
    return;
  }

  // folder 모드
  folderMode = true;
  sidebar.hidden = false;
  document.body.classList.add('has-sidebar');
  setEmptyState(true);
  setFilenameDisplay(info.root);
  initSidebarResizer();

  await initSidebar({
    onSelect: (relPath) => selectFile(relPath),
  });

  installLinkInterceptor();
  installHistoryNav();
  connectWebSocket('folder');

  // Deep-link 지원: URL path가 뷰어 대상 파일을 가리키면 해당 파일 자동 열기
  const initialPath = pathFromLocation();
  if (initialPath) {
    selectFile(initialPath, { pushHistory: false, hash: (window.location.hash || '').replace(/^#/, '') });
  }
}

bootstrap();
