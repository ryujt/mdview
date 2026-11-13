// pdf-export.js — PDF save Worker for the toolbar "Export" menu
//
// Exports the selected, currently rendered document as PDF.
// - Markdown/code viewer: hides the viewer UI with print-only styles (@media print) and opens browser printing (Save as PDF).
// - HTML preview: reopens the original HTML in a separate sandbox iframe (srcdoc) and prints only that document,
//   because the preview iframe is cross-origin so the parent cannot call print on it, and printing the parent only captures the visible part of the iframe.
// - PDF file: already a PDF, so the original file is downloaded as is.
// ⌘P / Ctrl+P go through the same path, giving the same result regardless of document type.

import { showToast } from './recorder.js';

const PRINT_DELAY_MS = 300;
const PRINT_START_MESSAGE = 'mdview-print-start';
const PRINT_MESSAGE = 'mdview-print-done';
// If the print frame does not send a start signal within this time, treat it as failed and clean up (CSP block, no load event, etc.).
const PRINT_START_TIMEOUT_MS = 30000;
const PRINT_FRAME_ID = 'pdf-export-frame';
const DEFAULT_BASE_NAME = 'document';

let getTarget = () => null;
let getMarkdownTarget = () => null;
let menuButton = null;
let menuList = null;
let exportItem = null;
let zipItem = null;
let zipBusy = false;
let printFrame = null;
let printStarted = false;
let printTimer = 0;

/**
 * Uses the file name from the path, minus its extension, as the default PDF name.
 * @param {string} path
 */
export function exportBaseName(path) {
  const base = String(path || '').split(/[\\/]/).pop() || '';
  const name = base.replace(/\.[^.]+$/, '') || base;
  return name.trim() || DEFAULT_BASE_NAME;
}

/**
 * Builds printable HTML: inserts a <base> to keep relative paths working and a print-on-load script.
 * A srcdoc document's base URL is the viewer page, so an existing <base href> is rewritten to an absolute URL
 * relative to the original location; otherwise the original document URL is inserted as <base>.
 * @param {string} html  Original HTML
 * @param {string} baseHref  Absolute URL of the original document
 * @param {string} [title]  Document title to use as the print dialog's default file name
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
      // Leave unresolvable hrefs as they are in the document.
    }
    const start = baseHrefMatch.index + baseHrefMatch[0].search(/\bhref\s*=/i);
    const end = baseHrefMatch.index + baseHrefMatch[0].length;
    out = source.slice(0, start) + `href="${escapeAttr(absolute)}"` + source.slice(end);
  } else {
    const at = headInsertIndex(source);
    out = source.slice(0, at) + `<base href="${escapeAttr(baseHref)}">` + source.slice(at);
  }
  // Insert before the last </body> to avoid '</body>' inside earlier script strings or comments.
  const bodyEnd = [...out.matchAll(/<\/body\s*>/gi)].pop();
  return bodyEnd ? out.slice(0, bodyEnd.index) + script + out.slice(bodyEnd.index) : out + script;
}

// Where to insert <base>: after <head> → after <html> → after a leading doctype → at the very start.
// Content before the doctype would render the document in quirks mode, so stay after the doctype.
function headInsertIndex(source) {
  for (const pattern of [/<head(\s[^>]*)?>/i, /<html(\s[^>]*)?>/i, /^\s*(?:<!--[\s\S]*?-->\s*)*<!doctype[^>]*>/i]) {
    const m = pattern.exec(source);
    if (m) return m.index + m[0].length;
  }
  return 0;
}

// JS string inside an inline script: escape '<' so '</script>' cannot close the script.
function scriptString(value) {
  return JSON.stringify(String(value)).replace(/</g, '\\u003c');
}

/**
 * Splits highlighted code HTML (spans + escaped text) into per-line HTML.
 * Spans that cross lines are closed at the end of a line and reopened on the next.
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
 * @param {{getTarget: () => ({kind: 'page'|'html'|'pdf', name: string, url?: string} | null), getMarkdownTarget?: () => ({name: string, markdown: string} | null)}} options
 */
export function initPdfExport(options) {
  getTarget = options.getTarget;
  getMarkdownTarget = options.getMarkdownTarget || (() => null);
  menuButton = document.getElementById('export-menu-button');
  menuList = document.getElementById('export-menu');
  exportItem = document.getElementById('export-pdf');
  zipItem = document.getElementById('export-zip');
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
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      e.preventDefault();
      openMenu();
      (e.key === 'ArrowUp' && zipItem ? zipItem : exportItem).focus();
    }
  });
  exportItem.addEventListener('click', () => {
    // Use aria-disabled instead of disabled to keep focus and the hint (title) while blocking only the action.
    if (exportItem.getAttribute('aria-disabled') === 'true') return;
    closeMenu();
    exportPdf();
  });
  zipItem?.addEventListener('click', () => {
    if (zipItem.getAttribute('aria-disabled') === 'true') return;
    closeMenu();
    exportMarkdownZip();
  });
  menuList.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      e.preventDefault();
      closeMenu();
      menuButton.focus();
    } else if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(e.key)) {
      e.preventDefault();
      const items = [...menuList.querySelectorAll('[role="menuitem"]')];
      const index = items.indexOf(document.activeElement);
      const next = e.key === 'Home' ? 0 : e.key === 'End' ? items.length - 1
        : (index + (e.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length;
      items[next]?.focus();
    }
  });
  // Close when focus leaves the menu (e.g. via Tab).
  menuList.addEventListener('focusout', (e) => {
    if (!menuList.hidden && !(e.relatedTarget && e.relatedTarget.closest('#export-menu-wrap'))) closeMenu();
  });
  document.addEventListener('click', (e) => {
    if (!menuList.hidden && !e.target.closest('#export-menu-wrap')) closeMenu();
  });
  document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && !menuList.hidden) closeMenu();
    // ⌘P (macOS) / Ctrl+P: if a document is open, handle it as a type-specific Save as PDF.
    // With the Korean IME active, e.key arrives as 'ㅔ', so also check the physical key (e.code).
    if (isPrintShortcut(e)) {
      if (!getTarget() || document.querySelector('dialog[open]')) return;
      e.preventDefault();
      exportPdf();
    }
  });
  // Build it in the print events so the same printable code view is used even when printing from the browser menu.
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
  exportItem.textContent = target && target.kind === 'pdf' ? 'Download original PDF' : 'Save as PDF…';
  exportItem.title = target ? '' : 'Select a document first';
  updateZipItem();
  menuList.hidden = false;
  menuButton.setAttribute('aria-expanded', 'true');
}

function updateZipItem() {
  if (!zipItem) return;
  const target = getMarkdownTarget();
  zipItem.setAttribute('aria-disabled', zipBusy || !target ? 'true' : 'false');
  zipItem.setAttribute('aria-busy', zipBusy ? 'true' : 'false');
  zipItem.textContent = zipBusy ? 'Creating ZIP…' : 'Save as Markdown ZIP…';
  zipItem.title = zipBusy ? 'Creating the ZIP' : target ? '' : 'Select a Markdown document first';
}

function closeMenu() {
  menuList.hidden = true;
  menuButton.setAttribute('aria-expanded', 'false');
}

/** Saves the full current Markdown source and its diagrams as a ZIP for ordinary viewers. */
export async function exportMarkdownZip() {
  if (zipBusy) return;
  const target = getMarkdownTarget();
  if (!target) {
    showToast('Select a Markdown document to save as ZIP first.', { error: true });
    return;
  }
  zipBusy = true;
  updateZipItem();
  showToast('Creating ZIP…');
  try {
    const { createMarkdownZip } = await import('./markdown-export.js');
    const { blob } = await createMarkdownZip(target.markdown);
    const url = URL.createObjectURL(blob);
    try {
      downloadOriginal(url, `${exportBaseName(target.name)}.zip`);
    } finally {
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    }
    showToast('Markdown ZIP downloaded.');
  } catch (error) {
    showToast(`ZIP creation failed: ${error.message || error}`, { error: true });
  } finally {
    zipBusy = false;
    updateZipItem();
  }
}

/** Exports the current document as PDF. */
export async function exportPdf() {
  const target = getTarget();
  if (!target) {
    showToast('Select a document to save as PDF first.', { error: true });
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

// The print dialog's default file name follows document.title, so change it temporarily and restore it.
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
    // For browsers that never fire afterprint
    setTimeout(restore, 0);
  }
}

// The code viewer keeps line numbers (gutter) and code in separate columns, so wrapping misaligns the numbers.
// While printing, build a table with one row per line so numbers stay aligned even when long lines wrap.
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
    showToast('Could not load the HTML document, so it cannot be saved as PDF.', { error: true });
    return;
  }
  removePrintFrame();
  const frame = document.createElement('iframe');
  frame.id = PRINT_FRAME_ID;
  frame.title = 'Document for PDF export';
  frame.setAttribute('aria-hidden', 'true');
  frame.tabIndex = -1;
  // Open without allow-same-origin to isolate document scripts from the viewer, allowing only the print call.
  frame.setAttribute('sandbox', 'allow-scripts allow-modals');
  frame.srcdoc = buildPrintableHtml(html, new URL(url, window.location.href).href, name);
  document.body.appendChild(frame);
  printFrame = frame;
  printStarted = false;
  // The print dialog may stay open for a long time, so only treat it as failed if the start signal never arrives.
  printTimer = setTimeout(() => {
    if (printFrame !== frame || printStarted) return;
    removePrintFrame();
    showToast('Failed to start PDF printing.', { error: true });
  }, PRINT_START_TIMEOUT_MS);
}

function removePrintFrame() {
  clearTimeout(printTimer);
  if (printFrame) printFrame.remove();
  printFrame = null;
  printStarted = false;
}
