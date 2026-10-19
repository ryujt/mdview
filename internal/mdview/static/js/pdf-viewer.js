// pdf-viewer.js — PDF 문서 렌더링 Worker
//
// pdf.js(CDN, 버전 고정)를 지연 로드해 페이지별 캔버스 + 텍스트 레이어를 그린다.
// 첫 페이지만 즉시 렌더하고 나머지는 같은 크기의 자리표시자로 문서 높이를 먼저
// 확보한 뒤, 뷰포트에 다가올 때 렌더한다 — 대형 PDF도 열자마자 스크롤을 복원할 수 있다.
// 텍스트 레이어가 있어야 본문 선택/복사가 가능하고, 녹화(recorder.js)가
// .pdf-page 요소의 data-page-number와 클릭 좌표로 참조 위치를 기록할 수 있다.

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
    // 로드 실패(네트워크 등) 시 다음 시도에서 다시 로드할 수 있게 한다.
    pdfjsPromise.catch(() => {
      pdfjsPromise = null;
    });
  }
  return pdfjsPromise;
}

/**
 * 진행 중인 PDF 로딩/렌더를 중단한다.
 * PDF가 아닌 문서로 전환하거나 선택을 해제할 때 호출해, 늦게 완료된
 * 렌더가 새 화면을 덮어쓰지 않게 한다 (renderPDF도 시작 시 호출).
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
 * PDF를 container에 렌더링한다. 첫 페이지 렌더와 전체 자리표시자 배치가
 * 끝나면 resolve된다 (문서 전체 높이가 확보되어 스크롤 복원이 가능한 시점).
 * 렌더 도중 다른 문서로 전환되면(새 renderPDF 호출 또는 cancelPdfRender) 조용히 중단한다.
 * @param {string} url  PDF 원본 URL (세그먼트 인코딩된 경로)
 * @param {HTMLElement} container  렌더링 대상 요소
 */
export async function renderPDF(url, container) {
  cancelPdfRender();
  const seq = renderSeq;
  showNotice(container, 'pdf-loading', 'PDF 불러오는 중…');

  let pdfjs;
  let doc;
  try {
    pdfjs = await loadPdfjs();
    if (seq !== renderSeq) return;
    activeLoadingTask = pdfjs.getDocument(url);
    doc = await activeLoadingTask.promise;
  } catch (err) {
    if (seq !== renderSeq) return;
    showNotice(container, 'pdf-error', `PDF를 불러오지 못했습니다: ${err && err.message ? err.message : err}`);
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
    if (seq !== renderSeq) return; // 전환으로 인한 취소는 정상 흐름
    const notice = document.createElement('div');
    notice.className = 'pdf-error';
    notice.textContent = 'PDF 페이지 렌더링 중 오류가 발생했습니다.';
    viewer.appendChild(notice);
  }
}

// 컨테이너 폭에 맞는 페이지 viewport를 계산한다 (fit-width).
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

// 아직 렌더되지 않은 페이지의 자리표시자. 첫 페이지 크기로 추정해 문서 높이를 확보한다.
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

// 자리표시자 페이지가 뷰포트에 다가오면 실제 렌더링한다.
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
    // 취소되었거나 페이지 렌더에 실패한 경우 — 자리표시자를 그대로 둔다
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
    // 텍스트 레이어 실패는 선택/복사만 제한될 뿐 캔버스 표시에는 영향 없음
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
