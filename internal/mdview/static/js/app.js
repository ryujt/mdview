import { renderMarkdown } from './markdown-renderer.js';
import { renderCode, isMarkdownPath, isCodeViewerPath } from './code-viewer.js';
import { renderPDF, isPdfPath, cancelPdfRender } from './pdf-viewer.js';
import { renderHTML, isHtmlPath } from './html-viewer.js';
import { initSidebar, highlightFile, revealFile } from './sidebar.js';
import { initSidebarResizer } from './sidebar-resizer.js';
import { initRecorder, noteFileOpen } from './recorder.js';
import { initPreviewMediaViewer } from './preview-media-viewer.js';
import { initSettings } from './settings.js';
import { initPdfExport } from './pdf-export.js';

const WS_PROTOCOL = 'ws:';
const API_MODE_PATH = '/api/mode';
const API_FILE_PATH = '/api/file';
const API_SELECT_PATH = '/api/select';
const RECONNECT_DELAY_MS = 1000;
const SCROLL_RESTORE_RETRY_MS = 150;

const preview = document.getElementById('preview');
initPreviewMediaViewer(preview);
const filenameDisplay = document.getElementById('filename-display');
const sidebar = document.getElementById('sidebar');
const emptyState = document.getElementById('empty-state');

const currentBlocksRef = { current: [], transformed: '' };

// Original text of the current file, used for recording events
const contentRef = { raw: '', file: null };

// Relative path the user selected in folder mode (slash-separated)
let activeRelPath = null;

// Current file name used to choose the rendering method (folder mode: relative path, file mode: absolute path)
let activeFileName = null;

let folderMode = false;

// /api/mode response (used as root/file info in recording metadata)
let modeInfo = null;

// Sequence number for discarding late responses to earlier requests on rapid clicks
let selectSeq = 0;

// Per-document scroll positions (relPath → window.scrollY). Kept only for the page session.
const scrollPositions = new Map();

// PDF cache-busting counter. Incremented when the active PDF changes on disk.
let pdfCacheBust = 0;
let htmlCacheBust = 0;

function getWebSocketURL() {
  const port = window.location.port;
  const host = window.location.hostname;
  return `${WS_PROTOCOL}//${host}:${port}/ws`;
}

function setEmptyState(visible) {
  emptyState.hidden = !visible;
  preview.hidden = visible;
}

// Displays the current file as HTML/PDF/Markdown or in the code viewer, depending on its type.
// Folder mode trusts the server whitelist and sends every other file to the code viewer;
// file mode sends only known code/config types to the code viewer and renders the rest as Markdown as before.
// If the active file is a PDF, the WS carries no text content (only a change signal),
// so it is re-rendered from its URL regardless of the call path.
function renderContent(text) {
  contentRef.file = activeFileName;
  const html = activeFileName && isHtmlPath(activeFileName);
  document.body.classList.toggle('html-preview', Boolean(html));
  if (html) {
    cancelPdfRender();
    setEmptyState(false);
    contentRef.raw = text;
    currentBlocksRef.current = [];
    currentBlocksRef.transformed = '';
    htmlCacheBust += 1;
    renderHTML(activeDocumentUrl(htmlCacheBust) + window.location.hash, preview);
    return;
  }
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

// Renders the active PDF from its original URL (folder: relative path, file: file name).
function renderActivePdf() {
  document.body.classList.remove('html-preview');
  setEmptyState(false);
  contentRef.raw = '';
  contentRef.file = activeFileName;
  currentBlocksRef.current = [];
  currentBlocksRef.transformed = '';
  return renderPDF(activeDocumentUrl(pdfCacheBust), preview);
}

function activeDocumentUrl(cacheBust) {
  const rel = folderMode ? activeRelPath : fileBaseName(activeFileName);
  const encoded = (rel || '').split('/').map(encodeURIComponent).join('/');
  return `/${encoded}?v=${cacheBust}`;
}

// PDF export target: null if no document is displayed.
function exportTarget() {
  if (!activeFileName || preview.hidden) return null;
  if (isHtmlPath(activeFileName)) return { kind: 'html', name: activeFileName, url: activeDocumentUrl(htmlCacheBust) };
  if (isPdfPath(activeFileName)) return { kind: 'pdf', name: activeFileName, url: activeDocumentUrl(pdfCacheBust) };
  return { kind: 'page', name: activeFileName };
}

// Export uses the full original text of the current file, not the rendered DOM.
// While switching files or after a failed read, the previous document is not saved under the new name.
function markdownExportTarget() {
  if (!activeFileName || preview.hidden || contentRef.file !== activeFileName || !isMarkdownPath(activeFileName)) return null;
  return { name: activeFileName, markdown: contentRef.raw };
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

// Save the current scroll position before leaving a document.
function rememberScroll() {
  if (activeRelPath) scrollPositions.set(activeRelPath, window.scrollY);
}

/**
 * Right after rendering, scrolls to the document's saved position (top if none).
 * If diagrams/images lay out asynchronously and the document grows late,
 * the first restore may be clamped, so retry only while the user has not interacted.
 */
function restoreScroll(relPath, hash) {
  if (hash) {
    // An anchor, if present, takes priority (the Markdown renderer generates heading ids)
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
 * Folder mode only: opens the file at a relative path and updates the view.
 * @param {string} relPath  Path relative to the root (slash-separated)
 * @param {{pushHistory?: boolean, hash?: string}} [options]
 */
async function selectFile(relPath, options = {}) {
  const { pushHistory = true, hash = '' } = options;
  const seq = ++selectSeq;
  rememberScroll();
  activeRelPath = relPath;
  activeFileName = relPath;
  contentRef.file = null;
  setFilenameDisplay(relPath);
  revealFile(relPath);

  // Update the URL bar (back-button support and shareable deep links)
  if (pushHistory) {
    const url = '/' + relPath.split('/').map(encodeURIComponent).join('/') + (hash ? '#' + hash : '');
    history.pushState({ path: relPath }, '', url);
  }

  try {
    // Tell the server the active file (WS broadcast target)
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
    // Show the status instead of failing silently when the server is down or the network is lost.
    if (seq !== selectSeq) return;
    showFileError(relPath);
  }
}

function showFileError(relPath) {
  contentRef.file = null;
  document.body.classList.remove('html-preview');
  const message = document.createElement('p');
  message.style.color = '#dc3545';
  message.textContent = `Failed to load file: ${relPath} (check the server connection)`;
  preview.replaceChildren(message);
  setEmptyState(false);
}

/**
 * Clears the selection and returns to the initial (empty) state.
 * Called when the active file is clicked again in the sidebar or Back navigates to '/'.
 */
function deselectFile({ pushHistory = true } = {}) {
  if (!activeRelPath) return;
  selectSeq += 1;
  cancelPdfRender();
  document.body.classList.remove('html-preview');
  rememberScroll();
  activeRelPath = null;
  activeFileName = null;
  contentRef.raw = '';
  contentRef.file = null;
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
 * Intercepts link clicks in the preview to implement cross-document navigation in folder mode.
 * External links, mailto and plain hashes keep their default behavior.
 */
function installLinkInterceptor() {
  preview.addEventListener('click', (e) => {
    const anchor = e.target.closest('a');
    if (!anchor) return;
    const href = anchor.getAttribute('href');
    if (!href) return;

    // Leave external schemes, protocol-relative URLs, mailto, etc. alone.
    if (/^([a-z][a-z0-9+.-]*:)?\/\//i.test(href)) return;
    if (/^[a-z][a-z0-9+.-]*:/i.test(href) && !href.startsWith('http:') && !href.startsWith('https:')) return;

    // Pure hash links keep the default behavior (scroll within the same page)
    if (href.startsWith('#')) return;

    const [pathPart, hashPart = ''] = href.split('#');
    if (!pathPart) return;

    // Resolve the relative path against the active file's directory
    const baseUrl = new URL(
      '/' + (activeRelPath || ''),
      window.location.origin
    );
    const resolved = new URL(pathPart, baseUrl);
    const relPath = decodeURIComponent(resolved.pathname.replace(/^\/+/, ''));
    if (!relPath) return;

    // Only intercept links with extensions the viewer can open.
    // (The tree is lazy-loaded, so decide by type rather than existence)
    if (!isMarkdownPath(relPath) && !isCodeViewerPath(relPath) && !isPdfPath(relPath)) return;

    e.preventDefault();
    selectFile(relPath, { hash: hashPart });
  });
}

// The server serves the SPA only for viewable file paths, so only check whether the path exists here.
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

  // Initialize the recording Worker: provides snapshots of the current view state.
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
    // "Open" action on the transcription-complete toast (folder mode only)
    openFile: (relPath) => {
      if (folderMode) selectFile(relPath);
    },
  });
  initPdfExport({ getTarget: exportTarget, getMarkdownTarget: markdownExportTarget });

  if (info.mode === 'file') {
    activeFileName = info.filename;
    setFilenameDisplay(info.filename);
    setEmptyState(false);
    if (isPdfPath(info.filename)) renderActivePdf();
    connectWebSocket('file');
    return;
  }

  // Folder mode
  folderMode = true;
  sidebar.hidden = false;
  document.body.classList.add('has-sidebar');
  setEmptyState(true);
  setFilenameDisplay(info.root);
  initSidebarResizer();

  // Keep the browser's automatic scroll restoration on popstate from conflicting with per-document manual restoration.
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

  // Deep-link support: automatically open the file if the URL path points to a viewable file
  const initialPath = pathFromLocation();
  if (initialPath) {
    selectFile(initialPath, { pushHistory: false, hash: (window.location.hash || '').replace(/^#/, '') });
  }
}

initSettings();
bootstrap();
