const STORAGE_KEY = 'mdview:sidebar-width';
const DEFAULT_WIDTH = 260;
const MIN_WIDTH = 160;
const MAX_WIDTH_RATIO = 0.7;

function clampWidth(width) {
  const max = Math.max(MIN_WIDTH, Math.floor(window.innerWidth * MAX_WIDTH_RATIO));
  return Math.min(max, Math.max(MIN_WIDTH, Math.round(width)));
}

function applyWidth(width) {
  document.documentElement.style.setProperty('--sidebar-width', `${width}px`);
}

function loadStoredWidth() {
  const raw = localStorage.getItem(STORAGE_KEY);
  if (!raw) return null;
  const parsed = parseInt(raw, 10);
  return Number.isFinite(parsed) ? parsed : null;
}

export function initSidebarResizer() {
  const resizer = document.getElementById('sidebar-resizer');
  if (!resizer) return;

  const stored = loadStoredWidth();
  if (stored !== null) applyWidth(clampWidth(stored));

  resizer.hidden = false;

  let dragging = false;
  let currentWidth = stored !== null ? clampWidth(stored) : DEFAULT_WIDTH;

  const onPointerMove = (event) => {
    if (!dragging) return;
    currentWidth = clampWidth(event.clientX);
    applyWidth(currentWidth);
  };

  const stopDrag = () => {
    if (!dragging) return;
    dragging = false;
    resizer.classList.remove('dragging');
    document.body.classList.remove('resizing-sidebar');
    document.removeEventListener('pointermove', onPointerMove);
    document.removeEventListener('pointerup', stopDrag);
    document.removeEventListener('pointercancel', stopDrag);
    localStorage.setItem(STORAGE_KEY, String(currentWidth));
  };

  resizer.addEventListener('pointerdown', (event) => {
    if (event.button !== 0 && event.pointerType === 'mouse') return;
    dragging = true;
    event.preventDefault();
    resizer.classList.add('dragging');
    document.body.classList.add('resizing-sidebar');
    document.addEventListener('pointermove', onPointerMove);
    document.addEventListener('pointerup', stopDrag);
    document.addEventListener('pointercancel', stopDrag);
  });

  resizer.addEventListener('dblclick', () => {
    currentWidth = DEFAULT_WIDTH;
    applyWidth(currentWidth);
    localStorage.setItem(STORAGE_KEY, String(currentWidth));
  });

  window.addEventListener('resize', () => {
    const clamped = clampWidth(currentWidth);
    if (clamped !== currentWidth) {
      currentWidth = clamped;
      applyWidth(currentWidth);
    }
  });
}
