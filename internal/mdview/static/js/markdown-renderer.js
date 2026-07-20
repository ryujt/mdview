import JobFlowToSVG from './jobflow.js';
import LayoutToHTML from './layout.js';
import { stateToMermaid } from './state.js';
import { navigationToMermaid } from './navigation.js';
import { parseMarkdownToBlocks, findTextFenceClose } from './block-parser.js';
import { processDiagramsInBlock } from './diagram.js';
import { fixCjkEmphasis } from './cjk-emphasis.js';
import { preprocessSmartMd, applySmartTableSpecs } from './smartmd.js';

let textFenceTokenizerApplied = false;

// ```text 펜스가 내부의 ```xxx 펜스(다이어그램 등 코드 예시)를 삼켜 하나의
// 일반 텍스트 코드 블록으로 렌더링되도록 marked 토크나이저를 확장한다.
// 정보 문자열이 있는 펜스는 중첩 열림, 맨몸 ```는 닫힘으로 세어 영역을 구분한다.
function ensureTextFenceTokenizer() {
  if (textFenceTokenizerApplied) return;
  textFenceTokenizerApplied = true;
  window.marked.use({
    tokenizer: {
      fences(src) {
        if (!/^(`{3,}|~{3,})[ \t]*text[ \t]*(\n|$)/i.test(src)) return false;
        const lines = src.split('\n');
        const close = findTextFenceClose(lines, 0);
        if (close === -1) return false; // 미종결 → 기본 동작에 맡김
        return {
          type: 'code',
          raw: lines.slice(0, close + 1).join('\n'),
          lang: 'text',
          text: lines.slice(1, close).join('\n'),
        };
      },
    },
  });
}

export function initMarkdownRenderer() {
  ensureTextFenceTokenizer();
  const renderer = new window.marked.Renderer();
  const originalCodeRenderer = renderer.code.bind(renderer);

  renderer.code = function (code, language) {
    const lang = (language || '').trim().toLowerCase();
    if (lang === 'jobflow') {
      try {
        const svg = JobFlowToSVG.generateSVG(code);
        const viewBox = svg.match(/viewBox="([^"]+)"/)?.[1];
        const modifiedSvg = viewBox
          ? svg.replace(/<svg[^>]*>/, `<svg viewBox="${viewBox}" class="w-full">`)
          : svg;
        return `<div class="jobflow-diagram my-6">${modifiedSvg}</div>`;
      } catch {
        return originalCodeRenderer(code, language);
      }
    }
    if (lang === 'layout') {
      const html = LayoutToHTML.generateHTML(code);
      return `<div class="layout-diagram my-6">${html}</div>`;
    }
    if (lang === 'state') {
      const mermaidCode = stateToMermaid(code);
      const escaped = mermaidCode.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
      return `<div class="state-diagram mermaid my-6">${escaped}</div>`;
    }
    if (lang === 'navigation') {
      const mermaidCode = navigationToMermaid(code);
      const escaped = mermaidCode.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
      return `<div class="navigation-diagram mermaid my-6">${escaped}</div>`;
    }
    if (lang === 'mermaid') {
      const escaped = code.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
      return `<div class="mermaid my-6">${escaped}</div>`;
    }
    return originalCodeRenderer(code, language);
  };

  return renderer;
}

export function renderSingleBlock(block, index) {
  const wrapper = document.createElement('div');
  wrapper.setAttribute('data-block-index', index);
  wrapper.setAttribute('data-block-hash', block.hash);
  wrapper.setAttribute('data-block-line', block.startLine);
  wrapper.setAttribute('data-block-type', block.type);
  if (block.type === 'empty') return wrapper;
  const renderer = initMarkdownRenderer();
  // 코드 펜스를 제외한 블록에만 CJK 인접 강조 보정을 적용한다.
  const content = block.type === 'code_fence' ? block.content : fixCjkEmphasis(block.content);
  wrapper.innerHTML = window.marked.parse(content, { renderer });
  return wrapper;
}

export function patchPreviewDOM(preview, newBlocks) {
  const existingElements = preview.querySelectorAll('[data-block-index]');
  const existingByHash = new Map();
  existingElements.forEach((el) => {
    const hash = el.getAttribute('data-block-hash');
    if (hash && !existingByHash.has(hash)) existingByHash.set(hash, el);
  });

  const fragment = document.createDocumentFragment();
  const reusedHashes = new Set();
  const newBlockElements = [];

  for (let i = 0; i < newBlocks.length; i++) {
    const block = newBlocks[i];
    if (existingByHash.has(block.hash) && !reusedHashes.has(block.hash)) {
      const existingEl = existingByHash.get(block.hash);
      existingEl.setAttribute('data-block-index', i);
      existingEl.setAttribute('data-block-line', block.startLine);
      fragment.appendChild(existingEl);
      reusedHashes.add(block.hash);
    } else {
      const newEl = renderSingleBlock(block, i);
      fragment.appendChild(newEl);
      newBlockElements.push(newEl);
    }
  }

  preview.innerHTML = '';
  preview.appendChild(fragment);
  applySmartTableSpecs(preview);
  newBlockElements.forEach((el) => processDiagramsInBlock(el));

  const newMermaids = preview.querySelectorAll('.mermaid:not([data-processed])');
  if (newMermaids.length > 0 && window.mermaid) {
    window.mermaid.run({ nodes: Array.from(newMermaids) }).catch(() => {});
  }
}

export function renderMarkdown(markdownContent, preview, currentBlocksRef) {
  if (!markdownContent || markdownContent.trim() === '') {
    preview.innerHTML = '';
    currentBlocksRef.current = [];
    currentBlocksRef.transformed = '';
    return;
  }
  const transformed = preprocessSmartMd(markdownContent);
  const newBlocks = parseMarkdownToBlocks(transformed);
  currentBlocksRef.current = newBlocks;
  // 블록 라인 번호의 기준 텍스트 (녹화 이벤트의 원본 라인 매핑에 사용)
  currentBlocksRef.transformed = transformed;
  patchPreviewDOM(preview, newBlocks);
}
