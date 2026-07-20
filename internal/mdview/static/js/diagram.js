import JobFlowToSVG from './jobflow.js';
import LayoutToHTML from './layout.js';
import { navigationToMermaid } from './navigation.js';
import { stateToMermaid } from './state.js';

const COPY_RESET_DELAY_MS = 2000;

function createCopyButton(text) {
  const button = document.createElement('button');
  button.className = 'copy-button';
  button.textContent = 'Copy';
  button.addEventListener('click', () => {
    navigator.clipboard.writeText(text).then(() => {
      button.textContent = 'Copied!';
      setTimeout(() => { button.textContent = 'Copy'; }, COPY_RESET_DELAY_MS);
      // 녹화 Worker가 복사 버튼 사용도 copy 이벤트로 기록할 수 있게 알린다.
      document.dispatchEvent(
        new CustomEvent('mdview:manualcopy', { detail: { text, el: button } })
      );
    });
  });
  return button;
}

export function processMermaidDiagram(codeElement) {
  const mermaidDiv = document.createElement('div');
  mermaidDiv.classList.add('mermaid', 'my-6');
  mermaidDiv.textContent = codeElement.textContent;
  const copyButton = createCopyButton(codeElement.textContent);
  const container = document.createElement('div');
  container.className = 'relative w-full';
  container.appendChild(mermaidDiv);
  container.appendChild(copyButton);
  codeElement.parentNode.replaceChild(container, codeElement);
}

export function processJobFlowDiagram(codeElement) {
  const jobFlowDiv = document.createElement('div');
  jobFlowDiv.classList.add('jobflow-diagram', 'my-6');
  const svg = JobFlowToSVG.generateSVG(codeElement.textContent);
  const viewBox = svg.match(/viewBox="([^"]+)"/)?.[1];
  const modifiedSvg = viewBox
    ? svg.replace(/<svg[^>]*>/, `<svg viewBox="${viewBox}" class="w-full">`)
    : svg;
  jobFlowDiv.innerHTML = modifiedSvg;
  const copyButton = createCopyButton(codeElement.textContent);
  const container = document.createElement('div');
  container.className = 'relative w-full';
  container.appendChild(jobFlowDiv);
  container.appendChild(copyButton);
  codeElement.parentNode.replaceChild(container, codeElement);
}

export function processNavigationDiagram(codeElement) {
  const navInput = codeElement.textContent;
  const mermaidCode = navigationToMermaid(navInput);
  const navDiv = document.createElement('div');
  navDiv.classList.add('navigation-diagram', 'mermaid', 'my-6');
  navDiv.textContent = mermaidCode;
  const copyButton = createCopyButton(navInput);
  const container = document.createElement('div');
  container.className = 'relative w-full';
  container.appendChild(navDiv);
  container.appendChild(copyButton);
  codeElement.parentNode.replaceChild(container, codeElement);
}

export function processStateDiagram(codeElement) {
  const stateInput = codeElement.textContent;
  const mermaidCode = stateToMermaid(stateInput);
  const stateDiv = document.createElement('div');
  stateDiv.classList.add('state-diagram', 'mermaid', 'my-6');
  stateDiv.textContent = mermaidCode;
  const copyButton = createCopyButton(stateInput);
  const container = document.createElement('div');
  container.className = 'relative w-full';
  container.appendChild(stateDiv);
  container.appendChild(copyButton);
  codeElement.parentNode.replaceChild(container, codeElement);
}

export function processLayoutDiagram(codeElement) {
  if (!document.getElementById('layout-styles')) {
    const styleEl = document.createElement('style');
    styleEl.id = 'layout-styles';
    styleEl.textContent = LayoutToHTML.generateStyle();
    document.head.appendChild(styleEl);
  }
  const layoutDiv = document.createElement('div');
  layoutDiv.classList.add('layout-diagram', 'my-6');
  layoutDiv.innerHTML = LayoutToHTML.generateHTML(codeElement.textContent);
  const copyButton = createCopyButton(codeElement.textContent);
  const container = document.createElement('div');
  container.className = 'relative w-full';
  container.appendChild(layoutDiv);
  container.appendChild(copyButton);
  codeElement.parentNode.replaceChild(container, codeElement);
}

export function processCodeBlock(codeElement, lang) {
  const hljs = window.hljs;
  const pre = codeElement.parentNode;
  const copyButton = createCopyButton(codeElement.textContent);
  const container = document.createElement('div');
  container.className = 'relative w-full';
  pre.parentNode.replaceChild(container, pre);
  container.appendChild(pre);
  container.appendChild(copyButton);
  // 언어가 명시되고 등록된 경우에만 하이라이트. 미표기 코드는 자동 탐지 없이 일반 텍스트 처리.
  if (hljs && lang && hljs.getLanguage && hljs.getLanguage(lang)) {
    try { hljs.highlightElement(codeElement); } catch (e) { /* ignore */ }
  }
}

export function processDiagramsInBlock(blockElement) {
  const codeBlocks = blockElement.querySelectorAll('pre code');
  codeBlocks.forEach((block) => {
    const classes = Array.from(block.classList);
    const langClass = classes.find((c) => c.startsWith('language-'));
    const lang = langClass ? langClass.replace('language-', '').trim().toLowerCase() : '';
    if (lang === 'mermaid') processMermaidDiagram(block);
    else if (lang === 'jobflow') processJobFlowDiagram(block);
    else if (lang === 'navigation') processNavigationDiagram(block);
    else if (lang === 'state') processStateDiagram(block);
    else if (lang === 'layout') processLayoutDiagram(block);
    else processCodeBlock(block, lang);
  });
}
