import { renderMarkdown } from './markdown-renderer.js';
import { renderCode, isMarkdownPath, isCodeViewerPath } from './code-viewer.js';
import { renderPDF, isPdfPath, cancelPdfRender } from './pdf-viewer.js';
import { initSidebar, highlightFile, revealFile } from './sidebar.js';
import { initSidebarResizer } from './sidebar-resizer.js';
import { initRecorder, noteFileOpen } from './recorder.js';
import { initWebOpen } from './webopen.js';

const WS_PROTOCOL = 'ws:';
const API_MODE_PATH = '/api/mode';
const API_FILE_PATH = '/api/file';
const API_SELECT_PATH = '/api/select';
const RECONNECT_DELAY_MS = 1000;
const SCROLL_RESTORE_RETRY_MS = 150;

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

// 문서별 스크롤 위치 (relPath → window.scrollY). 페이지 세션 동안만 유지된다.
const scrollPositions = new Map();

// PDF 캐시 무효화 카운터. 활성 PDF가 디스크에서 바뀌면 증가한다.
let pdfCacheBust = 0;

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
// 활성 파일이 PDF면 WS가 텍스트 내용을 싣지 않으므로(변경 신호만 수신)
// 어떤 호출 경로로 오든 URL에서 다시 렌더링한다.
function renderContent(text) {
  if (activeFileName && isPdfPath(activeFileName)) {
    pdfCacheBust += 1;
    renderActivePdf();
    return;
  }
  cancelPdfRender();
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

// 활성 PDF를 원본 URL에서 렌더링한다 (folder: 상대 경로, file: 파일명).
function renderActivePdf() {
  setEmptyState(false);
  contentRef.raw = '';
  currentBlocksRef.current = [];
  currentBlocksRef.transformed = '';
  return renderPDF(activePdfUrl(), preview);
}

function activePdfUrl() {
  const rel = folderMode ? activeRelPath : fileBaseName(activeFileName);
  const encoded = (rel || '').split('/').map(encodeURIComponent).join('/');
  return `/${encoded}?v=${pdfCacheBust}`;
}

function fileBaseName(p) {
  return (p || '').split(/[\\/]/).pop();
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

// 문서를 떠나기 전에 현재 스크롤 위치를 저장한다.
function rememberScroll() {
  if (activeRelPath) scrollPositions.set(activeRelPath, window.scrollY);
}

/**
 * 렌더 직후 해당 문서의 저장된 스크롤 위치로 이동한다 (없으면 맨 위).
 * 다이어그램/이미지가 비동기로 레이아웃되어 문서 높이가 늦게 늘어나면
 * 첫 복원이 클램프될 수 있으므로, 사용자가 손대지 않은 동안만 재시도한다.
 */
function restoreScroll(relPath, hash) {
  if (hash) {
    // 앵커가 있으면 앵커 스크롤 우선 (마크다운 렌더러가 heading id를 생성함)
    requestAnimationFrame(() => {
      const target = document.getElementById(hash) || document.querySelector(`[name="${CSS.escape(hash)}"]`);
      if (target) target.scrollIntoView({ behavior: 'smooth', block: 'start' });
    });
    return;
  }
  const saved = scrollPositions.get(relPath) || 0;
  window.scrollTo(0, saved);
  let applied = window.scrollY;
  if (applied >= saved) return;
  const retry = () => {
    if (activeRelPath !== relPath || window.scrollY !== applied) return;
    window.scrollTo(0, saved);
    applied = window.scrollY;
  };
  requestAnimationFrame(() => {
    retry();
    if (applied < saved) setTimeout(retry, SCROLL_RESTORE_RETRY_MS);
  });
}

/**
 * 폴더 모드 전용: 상대 경로의 파일을 열고 뷰를 갱신한다.
 * @param {string} relPath  루트 기준 상대 경로 (슬래시 구분)
 * @param {{pushHistory?: boolean, hash?: string}} [options]
 */
async function selectFile(relPath, options = {}) {
  const { pushHistory = true, hash = '' } = options;
  const seq = ++selectSeq;
  rememberScroll();
  activeRelPath = relPath;
  activeFileName = relPath;
  setFilenameDisplay(relPath);
  revealFile(relPath);

  // URL 바 갱신 (뒤로가기 지원 및 공유 가능한 딥링크)
  if (pushHistory) {
    const url = '/' + relPath.split('/').map(encodeURIComponent).join('/') + (hash ? '#' + hash : '');
    history.pushState({ path: relPath }, '', url);
  }

  try {
    // 서버에 활성 파일 지정 (WS 브로드캐스트 대상)
    await fetch(`${API_SELECT_PATH}?path=${encodeURIComponent(relPath)}`);
    if (seq !== selectSeq) return;

    if (isPdfPath(relPath)) {
      noteFileOpen(relPath);
      await renderActivePdf();
      if (seq !== selectSeq) return;
      restoreScroll(relPath, hash);
      return;
    }

    cancelPdfRender();
    const res = await fetch(`${API_FILE_PATH}?path=${encodeURIComponent(relPath)}`);
    if (seq !== selectSeq) return;
    if (!res.ok) {
      showFileError(relPath);
      return;
    }
    const text = await res.text();
    if (seq !== selectSeq) return;
    renderContent(text);
    noteFileOpen(relPath);
    restoreScroll(relPath, hash);
  } catch {
    // 서버 종료/네트워크 단절 시 조용히 실패하지 않고 상태를 보여준다.
    if (seq !== selectSeq) return;
    showFileError(relPath);
  }
}

function showFileError(relPath) {
  const message = document.createElement('p');
  message.style.color = '#dc3545';
  message.textContent = `파일을 불러오지 못했습니다: ${relPath} (서버 연결을 확인하세요)`;
  preview.replaceChildren(message);
  setEmptyState(false);
}

/**
 * 선택을 해제하고 초기(빈) 상태로 돌아간다.
 * 사이드바에서 활성 파일을 다시 클릭하거나 뒤로가기로 '/'에 도달하면 호출된다.
 */
function deselectFile({ pushHistory = true } = {}) {
  if (!activeRelPath) return;
  selectSeq += 1;
  cancelPdfRender();
  rememberScroll();
  activeRelPath = null;
  activeFileName = null;
  contentRef.raw = '';
  currentBlocksRef.current = [];
  currentBlocksRef.transformed = '';
  setFilenameDisplay(modeInfo ? modeInfo.root : '');
  highlightFile('');
  preview.replaceChildren();
  setEmptyState(true);
  window.scrollTo(0, 0);
  if (pushHistory) history.pushState({ path: null }, '', '/');
  fetch(`${API_SELECT_PATH}?path=`).catch(() => {});
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

    // 뷰어가 열 수 있는 확장자의 링크만 인터셉트한다.
    // (트리를 lazy 로딩하므로 존재 여부 대신 타입으로 판정한다)
    if (!isMarkdownPath(relPath) && !isCodeViewerPath(relPath) && !isPdfPath(relPath)) return;

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
    } else {
      deselectFile({ pushHistory: false });
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
  initWebOpen();

  if (info.mode === 'file') {
    activeFileName = info.filename;
    setFilenameDisplay(info.filename);
    setEmptyState(false);
    if (isPdfPath(info.filename)) renderActivePdf();
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

  // popstate 시 브라우저 자동 스크롤 복원이 문서별 수동 복원과 충돌하지 않게 한다.
  history.scrollRestoration = 'manual';

  await initSidebar({
    onSelect: (relPath) => {
      if (relPath === activeRelPath) deselectFile();
      else selectFile(relPath);
    },
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
