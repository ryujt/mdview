// pdf-export.js — 툴바 「내보내기」 메뉴의 PDF 저장 Worker
//
// 선택되어 렌더링 중인 문서를 PDF로 내보낸다.
// - 마크다운·코드뷰어: 인쇄 전용 스타일(@media print)로 뷰어 UI를 숨기고 브라우저 인쇄(PDF로 저장)를 연다.
// - HTML 미리보기: 원본 HTML을 별도 sandbox iframe(srcdoc)에 다시 열어 그 문서만 인쇄한다.
//   미리보기 iframe은 다른 출처라 부모가 인쇄를 호출할 수 없고, 부모 인쇄는 iframe이 보이는 영역만 담기 때문이다.
// - PDF 파일: 이미 PDF이므로 원본 파일을 그대로 다운로드한다.
// ⌘P / Ctrl+P도 같은 경로로 처리해 문서 종류와 무관하게 같은 결과를 낸다.

import { showToast } from './recorder.js';

const PRINT_DELAY_MS = 300;
const PRINT_START_MESSAGE = 'mdview-print-start';
const PRINT_MESSAGE = 'mdview-print-done';
// 인쇄 프레임이 이 시간 안에 시작 신호를 보내지 않으면 실패로 보고 정리한다(CSP 차단, load 미발생 등).
const PRINT_START_TIMEOUT_MS = 30000;
const PRINT_FRAME_ID = 'pdf-export-frame';
const DEFAULT_BASE_NAME = 'document';

let getTarget = () => null;
let menuButton = null;
let menuList = null;
let exportItem = null;
let printFrame = null;
let printStarted = false;
let printTimer = 0;

/**
 * 경로에서 확장자를 뺀 파일 이름을 PDF 기본 이름으로 쓴다.
 * @param {string} path
 */
export function exportBaseName(path) {
  const base = String(path || '').split(/[\\/]/).pop() || '';
  const name = base.replace(/\.[^.]+$/, '') || base;
  return name.trim() || DEFAULT_BASE_NAME;
}

/**
 * 인쇄용 HTML을 만든다: 상대 경로 유지를 위한 <base>와 로드 후 인쇄 스크립트를 넣는다.
 * srcdoc 문서의 기준 URL은 뷰어 페이지이므로, 문서에 <base href>가 있으면 원본 위치 기준 절대 URL로 바꾸고
 * 없으면 원본 문서 URL을 <base>로 넣는다.
 * @param {string} html  원본 HTML
 * @param {string} baseHref  원본 문서의 절대 URL
 * @param {string} [title]  인쇄 대화상자의 기본 파일 이름으로 쓸 문서 제목
 */
export function buildPrintableHtml(html, baseHref, title) {
  const source = String(html || '');
  const setTitle = title ? `document.title=${scriptString(title)};` : '';
  const script =
    `<script>addEventListener('load',function(){setTimeout(function(){` +
    `parent.postMessage(${JSON.stringify(PRINT_START_MESSAGE)},'*');` +
    `try{${setTitle}print()}finally{parent.postMessage(${JSON.stringify(PRINT_MESSAGE)},'*')}` +
    `},${PRINT_DELAY_MS})});</script>`;
  const baseHrefMatch = /<base\b[^>]*?\bhref\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+))/i.exec(source);
  let out;
  if (baseHrefMatch) {
    const value = baseHrefMatch[1] ?? baseHrefMatch[2] ?? baseHrefMatch[3];
    let absolute = value;
    try {
      absolute = new URL(value, baseHref).href;
    } catch {
      // 해석할 수 없는 href는 문서에 있는 그대로 둔다.
    }
    const start = baseHrefMatch.index + baseHrefMatch[0].search(/\bhref\s*=/i);
    const end = baseHrefMatch.index + baseHrefMatch[0].length;
    out = source.slice(0, start) + `href="${escapeAttr(absolute)}"` + source.slice(end);
  } else {
    const at = headInsertIndex(source);
    out = source.slice(0, at) + `<base href="${escapeAttr(baseHref)}">` + source.slice(at);
  }
  // 앞쪽 스크립트 문자열·주석의 '</body>'를 피하려고 마지막 </body> 앞에 넣는다.
  const bodyEnd = [...out.matchAll(/<\/body\s*>/gi)].pop();
  return bodyEnd ? out.slice(0, bodyEnd.index) + script + out.slice(bodyEnd.index) : out + script;
}

// <base>를 넣을 위치: <head> 뒤 → <html> 뒤 → 선행 doctype 뒤 → 맨 앞.
// doctype 앞에 내용이 생기면 문서가 quirks 모드로 렌더링되므로 doctype 뒤를 지킨다.
function headInsertIndex(source) {
  for (const pattern of [/<head(\s[^>]*)?>/i, /<html(\s[^>]*)?>/i, /^\s*(?:<!--[\s\S]*?-->\s*)*<!doctype[^>]*>/i]) {
    const m = pattern.exec(source);
    if (m) return m.index + m[0].length;
  }
  return 0;
}

// 인라인 스크립트 안의 JS 문자열: '</script>'로 스크립트가 닫히지 않게 '<'를 이스케이프한다.
function scriptString(value) {
  return JSON.stringify(String(value)).replace(/</g, '\\u003c');
}

/**
 * 하이라이트된 코드 HTML(span + 이스케이프된 텍스트)을 라인별 HTML로 나눈다.
 * 여러 줄에 걸친 span은 줄 끝에서 닫고 다음 줄에서 다시 연다.
 * @param {string} html
 * @returns {string[]}
 */
export function splitHighlightedLines(html) {
  const lines = [];
  const open = [];
  let current = '';
  for (const token of String(html || '').split(/(<span[^>]*>|<\/span>|\n)/)) {
    if (!token) continue;
    if (token === '\n') {
      lines.push(current + '</span>'.repeat(open.length));
      current = open.join('');
    } else if (token.startsWith('<span')) {
      open.push(token);
      current += token;
    } else if (token === '</span>') {
      open.pop();
      current += token;
    } else {
      current += token;
    }
  }
  lines.push(current);
  return lines;
}

function escapeAttr(value) {
  return String(value).replace(/&/g, '&amp;').replace(/"/g, '&quot;').replace(/</g, '&lt;');
}

/**
 * @param {{getTarget: () => ({kind: 'page'|'html'|'pdf', name: string, url?: string} | null)}} options
 */
export function initPdfExport(options) {
  getTarget = options.getTarget;
  menuButton = document.getElementById('export-menu-button');
  menuList = document.getElementById('export-menu');
  exportItem = document.getElementById('export-pdf');
  if (!menuButton || !menuList || !exportItem) return;

  menuButton.addEventListener('click', () => {
    if (!menuList.hidden) {
      closeMenu();
      return;
    }
    openMenu();
    exportItem.focus();
  });
  menuButton.addEventListener('keydown', (e) => {
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      openMenu();
      exportItem.focus();
    }
  });
  exportItem.addEventListener('click', () => {
    // disabled 대신 aria-disabled를 써서 포커스와 안내(title)는 유지하고 실행만 막는다.
    if (exportItem.getAttribute('aria-disabled') === 'true') return;
    closeMenu();
    exportPdf();
  });
  menuList.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      e.preventDefault();
      closeMenu();
      menuButton.focus();
    }
  });
  // Tab 등으로 포커스가 메뉴 밖으로 나가면 닫는다.
  menuList.addEventListener('focusout', (e) => {
    if (!menuList.hidden && !(e.relatedTarget && e.relatedTarget.closest('#export-menu-wrap'))) closeMenu();
  });
  document.addEventListener('click', (e) => {
    if (!menuList.hidden && !e.target.closest('#export-menu-wrap')) closeMenu();
  });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && !menuList.hidden) closeMenu();
    // ⌘P(macOS) / Ctrl+P: 문서가 열려 있으면 문서 종류별 PDF 저장으로 처리한다.
    // 한글 입력 상태에서는 e.key가 'ㅔ'로 오므로 물리 키(e.code)도 본다.
    if (isPrintShortcut(e)) {
      if (!getTarget() || document.querySelector('dialog[open]')) return;
      e.preventDefault();
      exportPdf();
    }
  });
  // 브라우저 메뉴로 인쇄해도 같은 인쇄용 코드뷰가 쓰이도록 인쇄 이벤트에서 만든다.
  window.addEventListener('beforeprint', buildCodePrintViews);
  window.addEventListener('afterprint', removeCodePrintViews);
  window.addEventListener('message', (e) => {
    if (!printFrame || e.source !== printFrame.contentWindow) return;
    if (e.data === PRINT_START_MESSAGE) printStarted = true;
    else if (e.data === PRINT_MESSAGE) removePrintFrame();
  });
}

function isPrintShortcut(e) {
  const platform = (navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || '';
  const mod = /mac|iphone|ipad/i.test(platform) ? e.metaKey : e.ctrlKey;
  if (!mod || e.altKey || e.shiftKey || e.isComposing) return false;
  return e.code === 'KeyP' || (typeof e.key === 'string' && e.key.toLowerCase() === 'p');
}

function openMenu() {
  const target = getTarget();
  exportItem.setAttribute('aria-disabled', target ? 'false' : 'true');
  exportItem.textContent = target && target.kind === 'pdf' ? '원본 PDF 다운로드' : 'PDF로 저장…';
  exportItem.title = target ? '' : '먼저 문서를 선택하세요';
  menuList.hidden = false;
  menuButton.setAttribute('aria-expanded', 'true');
}

function closeMenu() {
  menuList.hidden = true;
  menuButton.setAttribute('aria-expanded', 'false');
}

/** 현재 문서를 PDF로 내보낸다. */
export async function exportPdf() {
  const target = getTarget();
  if (!target) {
    showToast('PDF로 저장할 문서를 먼저 선택하세요.', { error: true });
    return;
  }
  const name = exportBaseName(target.name);
  if (target.kind === 'pdf') {
    downloadOriginal(target.url, `${name}.pdf`);
    return;
  }
  if (target.kind === 'html') {
    await printHtml(target.url, name);
    return;
  }
  printPage(name);
}

// 인쇄 대화상자의 기본 파일 이름은 document.title을 따르므로 잠시 바꿨다가 되돌린다.
function printPage(name) {
  const previous = document.title;
  document.title = name;
  const restore = () => {
    document.title = previous;
    window.removeEventListener('afterprint', restore);
  };
  window.addEventListener('afterprint', restore);
  try {
    window.print();
  } finally {
    // afterprint를 보내지 않는 브라우저 대비
    setTimeout(restore, 0);
  }
}

// 코드뷰어는 라인 번호(gutter)와 본문이 별도 열이라 줄바꿈하면 번호가 어긋난다.
// 인쇄 동안만 라인별 행으로 된 표를 만들어, 긴 줄을 줄바꿈해도 번호가 맞게 한다.
function buildCodePrintViews() {
  removeCodePrintViews();
  for (const viewer of document.querySelectorAll('#preview .code-viewer')) {
    const code = viewer.querySelector('.code-content code');
    if (!code) continue;
    const table = document.createElement('table');
    table.className = 'code-print';
    const body = document.createElement('tbody');
    splitHighlightedLines(code.innerHTML).forEach((line, i) => {
      const row = document.createElement('tr');
      const num = document.createElement('td');
      num.className = 'code-print-num';
      num.textContent = String(i + 1);
      const cell = document.createElement('td');
      cell.className = `code-print-line ${code.className}`;
      cell.innerHTML = line;
      row.append(num, cell);
      body.appendChild(row);
    });
    table.appendChild(body);
    viewer.after(table);
  }
}

function removeCodePrintViews() {
  for (const table of document.querySelectorAll('#preview .code-print')) table.remove();
}

function downloadOriginal(url, fileName) {
  const a = document.createElement('a');
  a.href = url;
  a.download = fileName;
  document.body.appendChild(a);
  a.click();
  a.remove();
}

async function printHtml(url, name) {
  let html;
  try {
    const res = await fetch(url, { cache: 'no-store' });
    if (!res.ok) throw new Error(String(res.status));
    html = await res.text();
  } catch {
    showToast('HTML 문서를 불러오지 못해 PDF로 저장할 수 없습니다.', { error: true });
    return;
  }
  removePrintFrame();
  const frame = document.createElement('iframe');
  frame.id = PRINT_FRAME_ID;
  frame.title = 'PDF 저장용 문서';
  frame.setAttribute('aria-hidden', 'true');
  frame.tabIndex = -1;
  // allow-same-origin 없이 열어 문서 스크립트를 뷰어와 격리하고, 인쇄 호출만 허용한다.
  frame.setAttribute('sandbox', 'allow-scripts allow-modals');
  frame.srcdoc = buildPrintableHtml(html, new URL(url, window.location.href).href, name);
  document.body.appendChild(frame);
  printFrame = frame;
  printStarted = false;
  // 인쇄 대화상자는 오래 열려 있을 수 있으므로, 시작 신호가 끝내 오지 않은 경우만 실패로 처리한다.
  printTimer = setTimeout(() => {
    if (printFrame !== frame || printStarted) return;
    removePrintFrame();
    showToast('PDF 인쇄를 시작하지 못했습니다.', { error: true });
  }, PRINT_START_TIMEOUT_MS);
}

function removePrintFrame() {
  clearTimeout(printTimer);
  if (printFrame) printFrame.remove();
  printFrame = null;
  printStarted = false;
}
