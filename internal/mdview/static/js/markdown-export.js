import { Marked } from '../vendor/marked.js';
import JobFlowToSVG from './jobflow.js';
import { navigationToMermaid } from './navigation.js';
import { stateToMermaid } from './state.js';
import { findTextFenceClose, isTextFenceOpener } from './block-parser.js';

// Render from source, rather than the visible preview, so paginated/hidden
// diagrams and edits made immediately before downloading are included.
export async function jobflowToPng(script) {
  const svg = JobFlowToSVG.generateSVG(script);
  const [, width, height] = svg.match(/viewBox="0 0 ([\d.]+) ([\d.]+)"/) || [];
  if (!(Number(width) > 0 && Number(height) > 0)) {
    throw new Error('The jobflow has no objects to draw.');
  }
  const scale = Math.min(2, 4096 / width, 4096 / height);
  const canvas = document.createElement('canvas');
  canvas.width = Math.max(1, Math.round(width * scale));
  canvas.height = Math.max(1, Math.round(height * scale));
  const context = canvas.getContext('2d');
  if (!context) throw new Error('Unable to create the image.');

  const url = URL.createObjectURL(new Blob([
    svg.replace('<svg ', '<svg style="font-family:system-ui, sans-serif" '),
  ], { type: 'image/svg+xml;charset=utf-8' }));
  try {
    const image = new Image();
    await new Promise((resolve, reject) => {
      image.onload = resolve;
      image.onerror = () => reject(new Error('Unable to create the jobflow image.'));
      image.src = url;
    });
    context.fillStyle = '#ffffff';
    context.fillRect(0, 0, canvas.width, canvas.height);
    context.drawImage(image, 0, 0, canvas.width, canvas.height);
    return canvas.toDataURL('image/png');
  } finally {
    URL.revokeObjectURL(url);
  }
}

// Marked handles container indentation, so list/blockquote diagrams use the same
// fenced-code rules as the preview. Keep source line locations to replace only
// diagrams rather than serializing (and changing) the rest of the Markdown.
function collectDiagramFences(source) {
  const parser = new Marked({
    tokenizer: {
      fences(src) {
        const lines = src.split('\n');
        const open = lines[0].match(/^( {0,3})(`{3,}|~{3,})([^\r\n]*)$/);
        if (!open || (open[2][0] === '`' && open[3].includes('`'))) return false;
        const [, indent, fence, info] = open;
        let close = -1;
        if (isTextFenceOpener(lines[0].trim())) {
          const unindented = lines.map(line => line.replace(/^ {0,3}/, ''));
          close = findTextFenceClose(unindented, 0);
        } else {
          close = lines.findIndex((line, index) => {
            const end = line.match(/^ {0,3}(`{3,}|~{3,})[ \t]*$/);
            return index > 0 && end && end[1][0] === fence[0] && end[1].length >= fence.length;
          });
        }
        const end = close === -1 ? lines.length - 1 : close;
        const body = lines.slice(1, close === -1 ? lines.length : close)
          .map(line => line.replace(new RegExp(`^ {0,${indent.length}}`), '')).join('\n');
        return { type: 'code', lang: info.trim(), raw: lines.slice(0, end + 1).join('\n'), text: body };
      },
    },
  });
  // Front matter is metadata, including any literal fenced examples in values.
  const normalized = source.replace(/\r\n?|\r/g, '\n').replace(
    /^---\n[\s\S]*?\n---(?:\n|$)/,
    match => match.replace(/[^\n]/g, ' '),
  );
  const diagrams = [];
  function visit(tokens, container, firstLine) {
    let cursor = 0;
    for (const token of tokens) {
      // Reference definitions are consumed by Marked without emitting tokens.
      const offset = container.indexOf(token.raw, cursor);
      if (offset === -1) continue;
      cursor = offset + token.raw.length;
      const start = firstLine + (container.slice(0, offset).match(/\n/g) || []).length;
      if (token.type === 'blockquote' || token.type === 'list_item') {
        visit(token.tokens, token.text, start);
      } else if (token.type === 'list') {
        visit(token.items, token.raw, start);
      } else if (token.type === 'code') {
        const lines = token.raw.split('\n');
        const open = lines[0].match(/^( {0,3})(`{3,}|~{3,})([^\r\n]*)$/);
        const language = (token.lang || '').trim().toLowerCase();
        if (!open || !['navigation', 'state', 'jobflow'].includes(language)) continue;
        const fence = open[2];
        const close = lines.findIndex((line, index) => {
          if (index === 0) return false;
          const end = line.match(/^ {0,3}(`{3,}|~{3,})[ \t]*$/);
          return end && end[1][0] === fence[0] && end[1].length >= fence.length;
        });
        if (close !== -1) diagrams.push({ start, end: start + close, fence, language, script: token.text });
      }
    }
  }
  visit(parser.lexer(normalized), normalized, 0);
  return diagrams;
}

export async function prepareMarkdownExport(source, { renderJobflow = jobflowToPng } = {}) {
  // Keep original line endings and all non-diagram text byte-for-byte.
  const lines = source.match(/[^\n]*\n|[^\n]+$/g) || [];
  const text = lines.map(line => line.replace(/\r?\n$/, ''));
  const output = [];
  const images = [];
  let cursor = 0;

  for (const { start, end, fence, language, script } of collectDiagramFences(source)) {
    output.push(...lines.slice(cursor, start));
    const prefix = text[start].slice(0, text[start].indexOf(fence));
    if (language === 'jobflow') {
      const index = images.length + 1;
      const filename = `jobflow-${index}.png`;
      const dataUrl = await renderJobflow(script);
      if (!dataUrl?.startsWith('data:image/png;base64,')) {
        throw new Error(`Unable to create the image for jobflow ${index}.`);
      }
      images.push({ filename, dataUrl });
      const ending = lines[end].slice(text[end].length);
      output.push(`${prefix}![Jobflow ${index}](images/${filename})${ending}`);
    } else {
      const convert = language === 'navigation' ? navigationToMermaid : stateToMermaid;
      const eol = lines[start].endsWith('\r\n') ? '\r\n' : '\n';
      // A list marker belongs only on the opening line; generated body lines
      // retain the continuation indentation and quote markers of the closer.
      const bodyPrefix = text[end].slice(0, text[end].indexOf(fence));
      const mermaid = convert(script).trimEnd().split('\n').map(line => bodyPrefix + line).join(eol);
      output.push(`${prefix}${fence}mermaid${eol}${mermaid}${eol}${lines[end]}`);
    }
    cursor = end + 1;
  }
  output.push(...lines.slice(cursor));
  return { markdown: output.join(''), images };
}

export async function createMarkdownZip(source) {
  const [{ default: JSZip }, { markdown, images }] = await Promise.all([
    import('../vendor/jszip.js'), prepareMarkdownExport(source),
  ]);
  const zip = new JSZip();
  zip.file('document.md', markdown);
  for (const { filename, dataUrl } of images) {
    zip.file(`images/${filename}`, dataUrl.split(',')[1], { base64: true });
  }
  return { blob: await zip.generateAsync({ type: 'blob' }), imageCount: images.length };
}
