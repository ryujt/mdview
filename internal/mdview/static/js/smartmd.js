// SmartMD preprocessor — mirrors the web app's src/lib/smartmd.js, adapted for
// the browser build where `marked` is a global (window.marked) loaded via CDN.
// Operates on raw markdown text and produces transformed markdown (with embedded
// HTML for callouts/figures/captions/frontmatter) that the block-based renderer
// can pass to marked.

import { fixCjkEmphasis } from './cjk-emphasis.js';
import { isTextFenceOpener, findTextFenceClose } from './block-parser.js';

function escapeHtml(s) {
  return String(s).replace(/[&<>"']/g, (c) => ({
    '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;',
  }[c]));
}

// Minimal YAML front matter parser — only handles top-level scalar keys for
// the metadata banner. Nested values are kept as their raw string.
function parseFrontMatterScalars(text) {
  const out = {};
  for (const rawLine of text.split('\n')) {
    const line = rawLine.replace(/\s+$/, '');
    if (!line || /^\s/.test(line)) continue;
    const m = line.match(/^([A-Za-z_][\w-]*)\s*:\s*(.*)$/);
    if (!m) continue;
    let v = m[2].trim();
    if (!v) continue;
    if ((v.startsWith('"') && v.endsWith('"')) || (v.startsWith("'") && v.endsWith("'"))) {
      v = v.slice(1, -1);
    }
    out[m[1]] = v;
  }
  return out;
}

function renderFrontMatterBanner(text) {
  const data = parseFrontMatterScalars(text);
  const keys = ['title', 'subtitle', 'author', 'date', 'locale', 'style'];
  const rows = keys
    .filter((k) => k in data)
    .map((k) => `<div><strong>${k}</strong>: ${escapeHtml(data[k])}</div>`)
    .join('');
  return `<div class="smart-frontmatter">${rows || '<em>front matter</em>'}</div>`;
}

// Walk source character-by-character, finding `{` at line start and matching
// a balanced `{...}` block (respecting string literals and {{var}} placeholders).
// If the block contains caption="...", emit a caption div in its place.
// {table ...} blocks emit a SMART_TABLE_SPEC comment marker for later DOM patching.
// Other attribute blocks are dropped (best-effort fallback).
function stripAttributeBlocks(src) {
  const masks = [];
  const masked = src.replace(/\{\{(!?[\w.-]+)\}\}/g, (m) => {
    masks.push(m);
    return `\x00${masks.length - 1}\x00`;
  });

  const out = [];
  let i = 0;
  const N = masked.length;
  while (i < N) {
    const atLineStart = i === 0 || masked[i - 1] === '\n';
    if (atLineStart && masked[i] === '{') {
      let depth = 0;
      let inStr = false;
      let j = i;
      let closed = false;
      while (j < N) {
        const ch = masked[j];
        if (inStr) {
          if (ch === '"' && masked[j - 1] !== '\\') inStr = false;
        } else {
          if (ch === '"') inStr = true;
          else if (ch === '{') depth++;
          else if (ch === '}') {
            depth--;
            if (depth === 0) { j++; closed = true; break; }
          }
        }
        j++;
      }
      if (!closed) { out.push(masked[i]); i++; continue; }
      const block = masked.slice(i, j);
      const cap = block.match(/caption="([^"]*)"/);
      if (cap) {
        const text = cap[1].replace(/\{n\}\.?\s*/g, '').trim();
        out.push(`\n\n<div class="smart-caption">${escapeHtml(text)}</div>\n\n`);
      }
      const isTable = /^\{table\b/.test(block);
      if (isTable) {
        const cols = parseColumnsSpec(block);
        if (cols.length) {
          const json = JSON.stringify(cols);
          out.push(`\n\n<!--SMART_TABLE_SPEC:${encodeURIComponent(json)}-->\n\n`);
        }
      } else if (/^\{align=/.test(block)) {
        const am = block.match(/align=(left|right|center)/);
        if (am) out.push(`\n\n<!--SMART_TABLE_ALIGN:${am[1]}-->\n\n`);
      }
      if (masked[j] === '\n') j++;
      i = j;
    } else {
      const nl = masked.indexOf('\n', i);
      const end = nl === -1 ? N : nl + 1;
      out.push(masked.slice(i, end));
      i = end;
    }
  }
  return out.join('').replace(/\x00(\d+)\x00/g, (_, n) => masks[+n]);
}

function parseColumnsSpec(block) {
  const startMatch = block.match(/columns\s*=\s*\[/);
  if (!startMatch) return [];
  let p = startMatch.index + startMatch[0].length;
  let depth = 1, inStr = false;
  let endIdx = -1;
  for (; p < block.length; p++) {
    const ch = block[p];
    if (inStr) { if (ch === '"' && block[p - 1] !== '\\') inStr = false; continue; }
    if (ch === '"') { inStr = true; continue; }
    if (ch === '[') depth++;
    else if (ch === ']') { depth--; if (depth === 0) { endIdx = p; break; } }
  }
  if (endIdx < 0) return [];
  const inner = block.slice(startMatch.index + startMatch[0].length, endIdx);
  const cols = [];
  depth = 0; let start = -1; inStr = false;
  for (let k = 0; k < inner.length; k++) {
    const ch = inner[k];
    if (inStr) { if (ch === '"' && inner[k - 1] !== '\\') inStr = false; continue; }
    if (ch === '"') { inStr = true; continue; }
    if (ch === '{') { if (depth === 0) start = k + 1; depth++; }
    else if (ch === '}') { depth--; if (depth === 0 && start >= 0) { cols.push(inner.slice(start, k)); start = -1; } }
  }
  return cols.map((c) => {
    const align = (c.match(/align\s*=\s*(left|right|center)/) || [])[1] || '';
    const width = (c.match(/width\s*=\s*([0-9.]+%?)/) || [])[1] || '';
    const title = (c.match(/title\s*=\s*"([^"]*)"/) || [])[1] || '';
    return { align, width, title };
  });
}

function renderDirective(name, attrs, inner) {
  const titleMatch = attrs.match(/title="([^"]*)"/);
  const captionMatch = attrs.match(/caption="([^"]*)"/);
  const title = titleMatch ? titleMatch[1] : '';
  const caption = captionMatch ? captionMatch[1] : '';
  // Pre-render inner markdown to HTML so the directive becomes a self-contained
  // HTML block (avoids blank-line / block-parser interactions).
  // 이 경로는 블록 렌더러를 거치지 않으므로 CJK 인접 강조 보정을 직접 적용한다.
  const innerHtml = window.marked.parse(fixCjkEmphasis(inner.trim()));

  const calloutTypes = ['success', 'warning', 'danger', 'info', 'note', 'tip'];
  if (calloutTypes.includes(name)) {
    const cls = ['success', 'warning', 'danger', 'info'].includes(name) ? name : 'info';
    return `<div class="smart-callout ${cls}">${title ? `<div class="smart-callout-title">${escapeHtml(title)}</div>` : ''}${innerHtml}</div>`;
  }
  if (name === 'figure') {
    return `<div class="smart-figure">${innerHtml}${caption ? `<div class="smart-figure-caption">${escapeHtml(caption)}</div>` : ''}</div>`;
  }
  return `<div class="smart-callout info"><div class="smart-callout-title">${escapeHtml(name)}${title ? ' — ' + escapeHtml(title) : ''}</div>${innerHtml}</div>`;
}

// ```text 영역은 SmartMD 변환 대상이 아니다 — 내부의 {attr}/{{var}}/::: 등이
// 원문 그대로 보여야 하므로, 영역을 한 줄짜리 자리표시자로 치환해 두었다가
// 모든 변환이 끝난 뒤 원문으로 되돌린다.
function maskTextFences(src) {
  const lines = src.split('\n');
  const regions = [];
  const out = [];
  for (let i = 0; i < lines.length; i++) {
    if (isTextFenceOpener(lines[i])) {
      const close = findTextFenceClose(lines, i);
      const end = close === -1 ? lines.length - 1 : close;
      regions.push(lines.slice(i, end + 1).join('\n'));
      out.push(`\x01TEXTFENCE_${regions.length - 1}\x01`);
      i = end;
    } else {
      out.push(lines[i]);
    }
  }
  return { masked: out.join('\n'), regions };
}

function restoreTextFences(src, regions) {
  return src.replace(/\x01TEXTFENCE_(\d+)\x01/g, (_, n) => regions[+n]);
}

// Public: preprocess raw markdown into marked-ready markdown.
export function preprocessSmartMd(src) {
  if (!src) return '';

  const { masked, regions } = maskTextFences(src);
  let body = masked;
  let frontmatterHtml = '';

  const fmMatch = body.match(/^---\n([\s\S]*?)\n---\n?/);
  if (fmMatch) {
    frontmatterHtml = renderFrontMatterBanner(fmMatch[1]);
    // Replace front matter with the banner + padding lines so subsequent line
    // numbers stay roughly aligned with the original source (helps scroll-sync).
    const consumedLines = fmMatch[0].split('\n').length - 1;
    const padding = '\n'.repeat(Math.max(0, consumedLines - 1));
    body = `${frontmatterHtml}${padding}\n${body.slice(fmMatch[0].length)}`;
  }

  // Strip image attribute suffix `![](x){width=...}` → `![](x)`
  body = body.replace(/(!\[[^\]]*\]\([^)]*\))\{[^}]*\}/g, '$1');

  // Strip Pandoc-style attribute blocks and emit caption / table markers.
  body = stripAttributeBlocks(body);

  // Highlight {{var}} placeholders for visibility.
  body = body.replace(/\{\{(!?[\w.-]+)\}\}/g, (_, k) => `<code>{{${k}}}</code>`);

  // ::: name [attrs] ... :::  (multiline). Wrap with blank lines so marked
  // treats the result as an HTML block.
  body = body.replace(
    /^:::[ \t]*(\w+)[ \t]*([^\n]*)\n([\s\S]*?)^:::[ \t]*$/gm,
    (_, name, attrs, inner) => `\n\n${renderDirective(name, attrs, inner)}\n\n`
  );

  return restoreTextFences(body, regions);
}

// Public: after markdown is rendered to DOM, apply table specs and align
// markers found in <!--SMART_TABLE_SPEC--> / <!--SMART_TABLE_ALIGN--> comments.
export function applySmartTableSpecs(root) {
  if (!root) return;
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_COMMENT);
  const comments = [];
  let n;
  while ((n = walker.nextNode())) comments.push(n);
  for (const c of comments) {
    const text = (c.nodeValue || '').trim();
    let spec = null;
    let alignAll = null;
    if (text.startsWith('SMART_TABLE_SPEC:')) {
      try { spec = JSON.parse(decodeURIComponent(text.slice('SMART_TABLE_SPEC:'.length))); } catch { /* ignore */ }
    } else if (text.startsWith('SMART_TABLE_ALIGN:')) {
      alignAll = text.slice('SMART_TABLE_ALIGN:'.length);
    } else {
      continue;
    }

    // Find the next <table> in document order. The comment may live inside
    // a different wrapper (block-by-block rendering), so walk via TreeWalker.
    let el = null;
    const elWalker = document.createTreeWalker(root, NodeFilter.SHOW_ELEMENT);
    elWalker.currentNode = c;
    let next;
    while ((next = elWalker.nextNode())) {
      if (next.tagName === 'TABLE') { el = next; break; }
    }
    if (!el) { c.remove(); continue; }

    if (spec) {
      const ths = el.querySelectorAll('thead th');
      spec.forEach((col, i) => {
        if (col.align) {
          if (ths[i]) ths[i].style.textAlign = col.align;
          el.querySelectorAll('tbody tr').forEach((row) => {
            const cell = row.children[i];
            if (cell) cell.style.textAlign = col.align;
          });
        }
        if (col.width && ths[i]) ths[i].style.width = col.width;
        if (col.title && ths[i]) ths[i].textContent = col.title;
      });
    }
    if (alignAll) {
      el.querySelectorAll('th, td').forEach((cell) => { cell.style.textAlign = alignAll; });
    }
    c.remove();
  }
}
