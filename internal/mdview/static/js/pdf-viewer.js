// pdf-viewer.js — PDF document rendering Worker
//
// Lazily loads pdf.js (CDN, pinned version) and draws a canvas + text layer per page.
// Only the first page is rendered immediately; the rest first reserve document height with
// same-size placeholders and render as they near the viewport — so even large PDFs can restore scroll on open.
// The text layer enables text selection/copy, and lets recording (recorder.js) log reference
// locations using the .pdf-page element's data-page-number and click coordinates.

const PDFJS_VERSION = '4.8.69';
const PDFJS_URL = `https://cdn.jsdelivr.net/npm/pdfjs-dist@${PDFJS_VERSION}/build/pdf.min.mjs`;
const PDFJS_WORKER_URL = `https://cdn.jsdelivr.net/npm/pdfjs-dist@${PDFJS_VERSION}/build/pdf.worker.min.mjs`;

const PDF_EXT = '.pdf';
const MAX_RENDER_SCALE = 2;
const FALLBACK_PAGE_WIDTH = 800;
const LAZY_RENDER_MARGIN = '1200px';

let pdfjsPromise = null;
let renderSeq = 0;
let activeLoadingTask = null;
let activeObserver = null;

export function isPdfPath(path) {
  return (path || '').toLowerCase().endsWith(PDF_EXT);
}

function loadPdfjs() {
  if (!pdfjsPromise) {
    pdfjsPromise = import(PDFJS_URL).then((lib) => {
      lib.GlobalWorkerOptions.workerSrc = PDFJS_WORKER_URL;
      return lib;
    });
    // On load failure (network, etc.), allow reloading on the next attempt.
    pdfjsPromise.catch(() => {
      pdfjsPromise = null;
    });
  }
  return pdfjsPromise;
}

/**
 * Cancels any in-progress PDF loading/rendering.
 * Called when switching to a non-PDF document or clearing the selection, so a late-finishing
 * render does not overwrite the new view (renderPDF also calls it on start).
 */
export function cancelPdfRender() {
  renderSeq += 1;
  if (activeObserver) {
    activeObserver.disconnect();
    activeObserver = null;
  }
  if (activeLoadingTask) {
    activeLoadingTask.destroy().catch(() => {});
    activeLoadingTask = null;
  }
}

/**
 * Renders a PDF into container. Resolves once the first page is rendered and all placeholders
 * are laid out (the point where the full document height is reserved and scroll can be restored).
 * Stops silently if another document is opened mid-render (a new renderPDF call or cancelPdfRender).
 * @param {string} url  Original PDF URL (segment-encoded path)
 * @param {HTMLElement} container  Element to render into
 */
export async function renderPDF(url, container) {
  cancelPdfRender();
  const seq = renderSeq;
  showNotice(container, 'pdf-loading', 'Loading PDF…');

  let pdfjs;
  let doc;
  try {
    pdfjs = await loadPdfjs();
    if (seq !== renderSeq) return;
    activeLoadingTask = pdfjs.getDocument(url);
    doc = await activeLoadingTask.promise;
  } catch (err) {
    if (seq !== renderSeq) return;
    showNotice(container, 'pdf-error', `Failed to load PDF: ${err && err.message ? err.message : err}`);
    return;
  }
  if (seq !== renderSeq) return;

  const viewer = document.createElement('div');
  viewer.className = 'pdf-viewer';
  container.replaceChildren(viewer);

  try {
    const firstPage = await doc.getPage(1);
    if (seq !== renderSeq) return;
    const viewport = fitViewport(firstPage, viewer);
    const firstEl = createPageElement(1, viewport);
    viewer.appendChild(firstEl);
    await renderPageInto(pdfjs, firstPage, firstEl, viewport);
    if (seq !== renderSeq) return;
    for (let n = 2; n <= doc.numPages; n++) {
      viewer.appendChild(createPendingPageElement(n, viewport));
    }
    observePendingPages(pdfjs, doc, viewer, seq);
  } catch {
    if (seq !== renderSeq) return; // Cancellation due to switching is a normal flow
    const notice = document.createElement('div');
    notice.className = 'pdf-error';
    notice.textContent = 'An error occurred while rendering a PDF page.';
    viewer.appendChild(notice);
  }
}

// Computes a page viewport that fits the container width (fit-width).
function fitViewport(page, viewer) {
  const base = page.getViewport({ scale: 1 });
  const availWidth = viewer.clientWidth || FALLBACK_PAGE_WIDTH;
  const scale = Math.min(availWidth / base.width, MAX_RENDER_SCALE);
  return page.getViewport({ scale });
}

function createPageElement(pageNumber, viewport) {
  const pageEl = document.createElement('div');
  pageEl.className = 'pdf-page';
  pageEl.dataset.pageNumber = String(pageNumber);
  sizePageElement(pageEl, viewport);
  return pageEl;
}

// Placeholder for a page not yet rendered. Reserves document height using the first page's size as an estimate.
function createPendingPageElement(pageNumber, estimatedViewport) {
  const pageEl = createPageElement(pageNumber, estimatedViewport);
  pageEl.classList.add('pdf-page-pending');
  return pageEl;
}

function sizePageElement(pageEl, viewport) {
  pageEl.style.width = `${Math.floor(viewport.width)}px`;
  pageEl.style.height = `${Math.floor(viewport.height)}px`;
  pageEl.style.setProperty('--scale-factor', String(viewport.scale));
}

// Render placeholder pages for real as they approach the viewport.
function observePendingPages(pdfjs, doc, viewer, seq) {
  const pending = viewer.querySelectorAll('.pdf-page-pending');
  if (pending.length === 0) return;
  const observer = new IntersectionObserver(
    (entries) => {
      entries.forEach((entry) => {
        if (!entry.isIntersecting) return;
        observer.unobserve(entry.target);
        renderPendingPage(pdfjs, doc, viewer, entry.target, seq);
      });
    },
    { rootMargin: LAZY_RENDER_MARGIN }
  );
  activeObserver = observer;
  pending.forEach((el) => observer.observe(el));
}

async function renderPendingPage(pdfjs, doc, viewer, pageEl, seq) {
  try {
    const page = await doc.getPage(Number(pageEl.dataset.pageNumber));
    if (seq !== renderSeq) return;
    const viewport = fitViewport(page, viewer);
    sizePageElement(pageEl, viewport);
    await renderPageInto(pdfjs, page, pageEl, viewport);
    if (seq !== renderSeq) return;
    pageEl.classList.remove('pdf-page-pending');
  } catch {
    // Cancelled or page render failed — leave the placeholder in place
  }
}

async function renderPageInto(pdfjs, page, pageEl, viewport) {
  const canvas = document.createElement('canvas');
  const outputScale = window.devicePixelRatio || 1;
  canvas.width = Math.floor(viewport.width * outputScale);
  canvas.height = Math.floor(viewport.height * outputScale);
  canvas.style.width = `${Math.floor(viewport.width)}px`;
  canvas.style.height = `${Math.floor(viewport.height)}px`;
  pageEl.appendChild(canvas);

  const transform = outputScale !== 1 ? [outputScale, 0, 0, outputScale, 0, 0] : undefined;
  await page.render({ canvasContext: canvas.getContext('2d'), transform, viewport }).promise;

  const textDiv = document.createElement('div');
  textDiv.className = 'pdf-text-layer';
  pageEl.appendChild(textDiv);
  try {
    if (pdfjs.TextLayer) {
      await new pdfjs.TextLayer({
        textContentSource: page.streamTextContent(),
        container: textDiv,
        viewport,
      }).render();
    } else if (pdfjs.renderTextLayer) {
      await pdfjs.renderTextLayer({
        textContentSource: page.streamTextContent(),
        container: textDiv,
        viewport,
      }).promise;
    }
  } catch {
    // A text layer failure only limits selection/copy; the canvas display is unaffected
  }
}

function showNotice(container, className, message) {
  const viewer = document.createElement('div');
  viewer.className = 'pdf-viewer';
  const notice = document.createElement('div');
  notice.className = className;
  notice.textContent = message;
  viewer.appendChild(notice);
  container.replaceChildren(viewer);
}
