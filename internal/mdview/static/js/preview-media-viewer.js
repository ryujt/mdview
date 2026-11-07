const DIAGRAM_SELECTOR = '.mermaid, .jobflow-diagram, .layout-diagram';
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
