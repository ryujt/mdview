// capture.js — 탐색 대상 페이지에 주입되는 조작 수집 스크립트
//
// 격리 월드(isolated world)에서 실행된다. 페이지의 JS는 이 스크립트의 전역도,
// CDP binding 함수도 볼 수 없으므로 가짜 이벤트를 주입하지 못한다.
// 대신 페이지가 navigator.clipboard.writeText로 하는 복사는 후킹할 수 없다 (알려진 제한).
//
// 이 스크립트는 무상태다 — 녹음 시작 시각을 모르고 Date.now()만 담아 보낸다.
// 경과 시간 계산과 절단·클램프의 정본은 Go(WebSession.Add)에 있고, 아래 상한은
// payload를 작게 만들기 위한 1차 절단일 뿐이다.

(() => {
  const BINDING = '__mdviewCaptureEvent__';
  const INSTALLED_FLAG = '__mdviewInstalled__';

  if (globalThis[INSTALLED_FLAG]) return;
  globalThis[INSTALLED_FLAG] = true;

  // Go 상수와 같은 값 (internal/mdview/websession.go). 정본은 Go 쪽이다.
  const MAX_URL = 500;
  const MAX_TITLE = 200;
  const MAX_SELECTOR = 300;
  const MAX_TAG = 40;
  const MAX_ID = 100;
  const MAX_CLASSES = 100;
  const MAX_TEXT = 200;
  const MAX_HEADING = 120;
  const MAX_CONTEXT = 300;
  const MAX_SELECTION = 200;

  const SELECTOR_MAX_DEPTH = 5;
  const SELECTOR_MAX_CLASSES = 2;
  const CONTEXT_MAX_ASCENT = 3;

  const HEADING_SELECTOR = 'h1,h2,h3,h4,h5,h6';
  const BLOCK_TAGS = new Set([
    'p', 'li', 'td', 'th', 'div', 'section', 'article', 'aside', 'header',
    'footer', 'main', 'blockquote', 'figure', 'dd', 'dt', 'form', 'fieldset',
  ]);
  const VALUE_TAGS = new Set(['input', 'textarea', 'select']);

  document.addEventListener('click', (e) => send('click', e), true);
  document.addEventListener('dblclick', (e) => send('dblclick', e), true);
  document.addEventListener('copy', (e) => send('copy', e), true);

  /** 이벤트를 payload로 만들어 binding으로 보낸다. 어떤 실패도 페이지에 영향을 주지 않는다. */
  function send(type, event) {
    try {
      const emit = globalThis[BINDING];
      if (typeof emit !== 'function') return;
      const el = elementOf(event.target);
      if (!el) return;
      emit(JSON.stringify(buildPayload(type, el)));
    } catch {
      /* 수집 실패가 페이지 동작을 방해해서는 안 된다 */
    }
  }

  function buildPayload(type, el) {
    const sensitive = isSensitive(el);
    return {
      type,
      atEpochMs: Date.now(),
      url: cut(location.href, MAX_URL),
      title: cut(document.title, MAX_TITLE),
      selector: cut(cssPath(el), MAX_SELECTOR),
      tag: cut(el.tagName.toLowerCase(), MAX_TAG),
      id: cut(el.id || '', MAX_ID),
      classes: cut(classAttr(el), MAX_CLASSES),
      text: sensitive ? '' : cut(textOf(el), MAX_TEXT),
      heading: cut(nearestHeading(el), MAX_HEADING),
      context: sensitive ? '' : cut(contextOf(el), MAX_CONTEXT),
      // selection은 dblclick·copy의 필드다. click까지 채우면 사용자가 어딘가 선택해 둔 채로
      // 계속 클릭할 때 모든 click에 같은 문자열이 붙어 잡음이 된다.
      selection: sensitive || type === 'click' ? '' : cut(selectionText(), MAX_SELECTION),
    };
  }

  /** 이벤트 대상에서 가장 가까운 요소를 얻는다 (텍스트 노드는 부모로 올린다). */
  function elementOf(node) {
    if (!node) return null;
    const el = node.nodeType === Node.ELEMENT_NODE ? node : node.parentElement;
    return el && el.tagName ? el : null;
  }

  /**
   * 비밀번호 입력란과 폼 입력값은 수집하지 않는다.
   * 입력 중인 검색어·자격 증명이 프롬프트로 새어 나가는 것을 막는다.
   */
  function isSensitive(el) {
    if (VALUE_TAGS.has(el.tagName.toLowerCase())) return true;
    return Boolean(el.closest && el.closest('input[type="password"]'));
  }

  /**
   * 요소를 알아볼 CSS 경로를 만든다.
   * "다시 클릭할 수 있는 정확한 경로"가 아니라 "AI가 무엇을 가리켰는지 알아볼 단서"가 목적이므로
   * id를 만나면 즉시 종료하고 깊이도 제한한다.
   */
  function cssPath(el) {
    const parts = [];
    let node = el;
    for (let depth = 0; node && node.tagName && depth < SELECTOR_MAX_DEPTH; depth++) {
      if (node.id) {
        parts.unshift(`#${cssEscape(node.id)}`);
        break;
      }
      parts.unshift(nodeSelector(node));
      node = node.parentElement;
    }
    return parts.join(' > ');
  }

  function nodeSelector(node) {
    let selector = node.tagName.toLowerCase();
    for (const cls of classList(node).slice(0, SELECTOR_MAX_CLASSES)) {
      selector += `.${cssEscape(cls)}`;
    }
    const index = siblingIndex(node);
    if (index > 0) selector += `:nth-of-type(${index})`;
    return selector;
  }

  /** 같은 태그의 형제가 여럿일 때만 1부터 시작하는 순번을 반환한다 (아니면 0). */
  function siblingIndex(node) {
    const parent = node.parentElement;
    if (!parent) return 0;
    const sameTag = [];
    for (const child of parent.children) {
      if (child.tagName === node.tagName) sameTag.push(child);
    }
    if (sameTag.length < 2) return 0;
    return sameTag.indexOf(node) + 1;
  }

  /**
   * class 속성 문자열을 얻는다.
   * SVG 요소의 className은 문자열이 아니라 SVGAnimatedString이라 그대로 쓰면 값이 깨진다.
   */
  function classAttr(el) {
    const value = el.getAttribute ? el.getAttribute('class') : '';
    return typeof value === 'string' ? value : '';
  }

  function classList(el) {
    return classAttr(el).split(/\s+/).filter(Boolean);
  }

  function cssEscape(value) {
    return globalThis.CSS && CSS.escape ? CSS.escape(value) : value;
  }

  /** innerText는 강제 리플로우를 유발하므로 textContent + 공백 정규화를 쓴다. */
  function textOf(el) {
    return normalize(el.textContent || '');
  }

  /** 문서 순서상 요소보다 앞에 있는 가장 가까운 제목을 찾는다. */
  function nearestHeading(el) {
    // querySelectorAll은 문서 순서로 돌려주므로 조건을 만족하는 마지막 제목이 가장 가깝다.
    // 요소가 제목 안에 들어 있는 경우에도 FOLLOWING 비트가 함께 켜진다.
    let found = '';
    for (const h of document.querySelectorAll(HEADING_SELECTOR)) {
      if (!(h.compareDocumentPosition(el) & Node.DOCUMENT_POSITION_FOLLOWING)) continue;
      found = normalize(h.textContent || '');
    }
    return found;
  }

  /** 요소에서 위로 올라가며 만난 첫 블록 요소의 텍스트를 맥락으로 쓴다. */
  function contextOf(el) {
    let node = el.parentElement;
    for (let up = 0; node && up < CONTEXT_MAX_ASCENT; up++) {
      if (BLOCK_TAGS.has(node.tagName.toLowerCase())) return normalize(node.textContent || '');
      node = node.parentElement;
    }
    return '';
  }

  /**
   * 복사·더블클릭 시점에 선택되어 있던 문자열을 얻는다.
   * copy 이벤트의 clipboardData는 쓰기 전용이라 실제 클립보드 내용은 읽을 수 없다.
   */
  function selectionText() {
    const sel = document.getSelection();
    return sel ? normalize(sel.toString()) : '';
  }

  function normalize(text) {
    return text.replace(/\s+/g, ' ').trim();
  }

  function cut(text, max) {
    const value = typeof text === 'string' ? text : '';
    return value.length > max ? value.slice(0, max) : value;
  }
})();
