const DIAGRAM_SELECTOR = '.mermaid, .jobflow-diagram, .layout-diagram';
const COPY_ICON = '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v1"/></svg>';
const COPIED_ICON = '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m5 12 5 5L20 7"/></svg>';
const COPY_LABEL = '다이어그램 코드 복사';
const COPY_RESET_DELAY_MS = 2000;
const EXPAND_ICON = '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><path d="M8 3H3v5m13-5h5v5M3 16v5h5m13-5v5h-5M3 3l6 6m12-6-6 6M3 21l6-6m12 6-6-6"/></svg>';

export function initPreviewMediaViewer(preview) {
  let selection = null;

  function close() {
    if (!selection) return;
    const { node, wrapper, button, dialog } = selection;
    selection = null;
    dialog.close();
    wrapper.prepend(node);
    wrapper.style.minHeight = '';
    wrapper.style.minWidth = '';
    dialog.remove();
    if (button.isConnected) button.focus({ preventScroll: true });
  }

  function open(node, wrapper, button) {
    close();
    const dialog = document.createElement('dialog');
    dialog.className = 'preview-media-dialog';
    dialog.setAttribute('aria-label', node.tagName === 'IMG' ? '이미지 전체 화면' : '다이어그램 전체 화면');
    const closeButton = document.createElement('button');
    closeButton.type = 'button';
    closeButton.className = 'preview-media-close';
    closeButton.setAttribute('aria-label', '전체 화면 닫기');
    closeButton.title = '닫기 (Esc)';
    closeButton.autofocus = true;
    closeButton.innerHTML = '<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><path d="m6 6 12 12M6 18 18 6"/></svg>';
    closeButton.addEventListener('click', close);
    dialog.addEventListener('cancel', (event) => { event.preventDefault(); close(); });
    dialog.addEventListener('close', () => { if (selection?.dialog === dialog) close(); });
    dialog.addEventListener('keydown', (event) => {
      event.stopPropagation();
      if (event.key !== 'Tab') return;
      const controls = Array.from(dialog.querySelectorAll('button, a[href], input, select, textarea, [tabindex]'))
        .filter((el) => el.tabIndex >= 0 && el.getClientRects().length > 0);
      const first = controls[0];
      const last = controls[controls.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last?.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first?.focus();
      }
    });
    const stage = document.createElement('div');
    stage.className = 'preview-media-stage';
    // Keep SVG references unique and reserve the document's scroll position.
    const bounds = wrapper.getBoundingClientRect();
    wrapper.style.minHeight = `${bounds.height}px`;
    wrapper.style.minWidth = `${bounds.width}px`;
    stage.append(node);
    dialog.append(closeButton, stage);
    document.body.append(dialog);
    selection = { node, wrapper, button, dialog };
    dialog.showModal();
  }

  function decorate() {
    // A file switch or live update may replace the selected block entirely.
    if (selection && !preview.contains(selection.wrapper)) close();
    preview.querySelectorAll(`${DIAGRAM_SELECTOR}, img`).forEach((node) => {
      if (node.parentElement?.closest('.preview-media')) return;
      if (node.tagName === 'IMG' && node.closest(DIAGRAM_SELECTOR)) return;
      if (node.tagName !== 'IMG' && !node.querySelector('svg, .layout-preview')) return;
      const wrapper = document.createElement(node.tagName === 'IMG' ? 'span' : 'div');
      wrapper.className = `preview-media${node.tagName === 'IMG' ? ' preview-media-image' : ''}`;
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'preview-media-expand';
      button.title = node.tagName === 'IMG' ? '이미지 전체 화면 보기' : '다이어그램 전체 화면 보기';
      button.setAttribute('aria-label', button.title);
      button.innerHTML = EXPAND_ICON;
      node.before(wrapper);
      wrapper.append(node, button);
      // 다이어그램은 확대 버튼 왼쪽에 원본 코드 복사 버튼을 둔다.
      if (node.tagName !== 'IMG' && node.dataset.source != null) {
        const copyButton = document.createElement('button');
        copyButton.type = 'button';
        copyButton.className = 'preview-media-copy';
        copyButton.title = COPY_LABEL;
        copyButton.setAttribute('aria-label', COPY_LABEL);
        copyButton.innerHTML = COPY_ICON;
        wrapper.append(copyButton);
      }
      if (node.tagName === 'IMG') {
        // SVG images without explicit dimensions can collapse in an inline wrapper.
        const ensureImageWidth = () => {
          if (node.naturalWidth && wrapper.getBoundingClientRect().width === 0) {
            wrapper.style.width = `${node.naturalWidth}px`;
          }
        };
        ensureImageWidth();
        if (!node.complete) node.addEventListener('load', ensureImageWidth, { once: true });
      }
    });
  }

  preview.addEventListener('click', (event) => {
    const copyButton = event.target.closest('.preview-media-copy');
    if (copyButton && preview.contains(copyButton)) {
      event.preventDefault();
      event.stopPropagation();
      copyDiagramSource(copyButton);
      return;
    }
    const button = event.target.closest('.preview-media-expand');
    if (!button || !preview.contains(button)) return;
    event.preventDefault();
    event.stopPropagation();
    open(button.parentElement.firstElementChild, button.parentElement, button);
  });
  const observer = new MutationObserver(decorate);
  observer.observe(preview, { childList: true, subtree: true });
  decorate();
}

/**
 * 다이어그램 코드를 시작·끝 펜스(```lang ... ```)까지 포함한 마크다운 코드 블록으로 만든다.
 * 코드 안에 백틱 연속이 있으면 그보다 긴 펜스를 쓴다.
 */
export function fencedSource(code, lang) {
  const body = String(code).replace(/\n+$/, '');
  const longest = Math.max(0, ...(body.match(/`+/g) || []).map((run) => run.length));
  const fence = '`'.repeat(Math.max(3, longest + 1));
  return `${fence}${lang}\n${body}\n${fence}`;
}

async function copyDiagramSource(button) {
  const node = button.parentElement?.firstElementChild;
  const text = fencedSource(node?.dataset.source ?? '', node?.dataset.lang ?? '');
  try {
    await writeClipboard(text);
  } catch {
    button.title = '복사하지 못했습니다';
    return;
  }
  button.innerHTML = COPIED_ICON;
  button.classList.add('copied');
  button.title = '복사됨';
  clearTimeout(button._resetTimer);
  button._resetTimer = setTimeout(() => {
    button.innerHTML = COPY_ICON;
    button.classList.remove('copied');
    button.title = COPY_LABEL;
  }, COPY_RESET_DELAY_MS);
  // 녹화 Worker가 다이어그램 복사도 copy 이벤트로 기록할 수 있게 알린다.
  document.dispatchEvent(new CustomEvent('mdview:manualcopy', { detail: { text, el: button } }));
}

async function writeClipboard(text) {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text);
      return;
    } catch {
      /* 권한·포커스 문제면 아래 방식으로 재시도 */
    }
  }
  const area = document.createElement('textarea');
  area.value = text;
  area.setAttribute('readonly', '');
  area.style.cssText = 'position:fixed;top:0;left:-9999px;opacity:0';
  document.body.append(area);
  area.select();
  const ok = document.execCommand('copy');
  area.remove();
  if (!ok) throw new Error('copy failed');
}
