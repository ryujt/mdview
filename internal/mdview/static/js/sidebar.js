// sidebar.js — 파일 트리 사이드바 Worker
//
// 트리 전체를 한 번에 로딩하지 않고, 폴더가 펼쳐질 때마다 해당 디렉토리의
// 1레벨 목록만 /api/tree?path= 로 요청한다. 같은 폴더를 다시 펼치면 새로 고친다.

const API_TREE_PATH = '/api/tree';

const tree = document.getElementById('tree');

let selectHandler = () => {};
let activePath = '';
let rootDetails = null;

async function fetchLevel(path) {
  const res = await fetch(`${API_TREE_PATH}?path=${encodeURIComponent(path)}`);
  if (!res.ok) throw new Error(`tree fetch failed: ${res.status}`);
  return res.json();
}

function buildFileButton(node) {
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'tree-file';
  btn.textContent = node.name;
  btn.dataset.path = node.path;
  if (node.path === activePath) btn.classList.add('active');
  btn.addEventListener('click', () => {
    selectHandler(node.path);
  });
  return btn;
}

function buildDirElement(node) {
  const details = document.createElement('details');
  details.className = 'tree-dir';
  details.dataset.path = node.path;

  const summary = document.createElement('summary');
  summary.className = 'tree-dir-label';
  summary.textContent = node.name + '/';
  details.appendChild(summary);

  const list = document.createElement('ul');
  list.className = 'tree-list';
  details.appendChild(list);

  details.addEventListener('toggle', () => {
    // 코드로 연 경우(openLoaded)는 이미 최신 목록이므로 재요청하지 않는다.
    if (details._suppressNextToggle) {
      details._suppressNextToggle = false;
      return;
    }
    if (details.open) loadChildren(details);
  });
  return details;
}

function populate(details, node, openPaths = null) {
  const list = details.querySelector(':scope > .tree-list');
  list.replaceChildren();
  (node.children || []).forEach((child) => {
    const li = document.createElement('li');
    li.appendChild(child.isDir ? buildDirElement(child) : buildFileButton(child));
    list.appendChild(li);
  });
  restoreOpenDirs(list, openPaths);
}

// 목록을 새로 채운 뒤, 갱신 전에 펼쳐져 있던 하위 폴더를 다시 로드해 펼친다.
function restoreOpenDirs(list, openPaths) {
  if (!openPaths || openPaths.size === 0) return;
  list.querySelectorAll(':scope > li > details.tree-dir').forEach((dir) => {
    if (!openPaths.has(dir.dataset.path)) return;
    loadChildren(dir, openPaths).then(() => openLoaded(dir));
  });
}

// 갱신으로 사라지기 전, 이 폴더 아래에서 펼쳐져 있던 경로들을 기억한다.
function collectOpenPaths(details) {
  return new Set(
    [...details.querySelectorAll('details.tree-dir[open]')].map((d) => d.dataset.path)
  );
}

// 같은 폴더의 동시 요청은 진행 중인 fetch 하나로 합친다.
function loadChildren(details, preserveOpenPaths = null) {
  if (details._childrenPromise) return details._childrenPromise;
  const openPaths = preserveOpenPaths || collectOpenPaths(details);
  details._childrenPromise = fetchLevel(details.dataset.path)
    .then((node) => {
      populate(details, node, openPaths);
    })
    .catch(() => {
      showLoadError(details);
    })
    .finally(() => {
      details._childrenPromise = null;
    });
  return details._childrenPromise;
}

function showLoadError(details) {
  const list = details.querySelector(':scope > .tree-list');
  const li = document.createElement('li');
  const msg = document.createElement('div');
  msg.className = 'tree-error';
  msg.textContent = '목록을 불러오지 못했습니다.';
  li.appendChild(msg);
  list.replaceChildren(li);
}

// toggle 핸들러의 재요청 없이 <details>를 펼친다 (이미 목록이 채워진 경우).
function openLoaded(details) {
  if (details.open) return;
  details._suppressNextToggle = true;
  details.open = true;
}

// 닫혀 있으면 목록을 로드한 뒤 펼치고, 이미 열려 있으면 로드 완료만 기다린다.
async function ensureOpen(details) {
  if (details.open) {
    if (details._childrenPromise) await details._childrenPromise;
    return;
  }
  await loadChildren(details);
  openLoaded(details);
}

export async function initSidebar({ onSelect }) {
  selectHandler = onSelect;
  let root;
  try {
    root = await fetchLevel('');
  } catch {
    tree.innerHTML = '<div class="tree-error">트리를 불러오지 못했습니다.</div>';
    return;
  }
  rootDetails = buildDirElement(root);
  populate(rootDetails, root);
  openLoaded(rootDetails);
  tree.replaceChildren(rootDetails);
}

/** 활성 파일 표시를 갱신한다. 빈 값이면 모든 활성 표시를 지운다. */
export function highlightFile(relPath) {
  activePath = relPath || '';
  applyActiveHighlight();
}

/**
 * 조상 폴더들을 순서대로 로드/펼침한 뒤 해당 파일을 활성 표시한다.
 * lazy 트리에서는 딥링크·문서 간 링크로 연 파일의 조상이 아직 로드되지
 * 않았을 수 있어 highlightFile 대신 이 함수를 사용한다.
 */
export async function revealFile(relPath) {
  activePath = relPath || '';
  applyActiveHighlight();
  if (!activePath || !rootDetails) return;
  try {
    await ensureOpen(rootDetails);
    const parts = activePath.split('/');
    let prefix = '';
    for (let i = 0; i < parts.length - 1; i++) {
      prefix = prefix ? `${prefix}/${parts[i]}` : parts[i];
      const dir = tree.querySelector(`details.tree-dir[data-path="${cssEscapeAttr(prefix)}"]`);
      if (!dir) return;
      await ensureOpen(dir);
    }
  } catch {
    return; // 트리 로드 실패 시 하이라이트만 생략한다
  }
  applyActiveHighlight();
}

function applyActiveHighlight() {
  tree.querySelectorAll('.tree-file.active').forEach((el) => el.classList.remove('active'));
  if (!activePath) return;
  const btn = tree.querySelector(`.tree-file[data-path="${cssEscapeAttr(activePath)}"]`);
  if (!btn) return;
  btn.classList.add('active');
  btn.scrollIntoView({ block: 'nearest' });
}

function cssEscapeAttr(value) {
  // 속성 셀렉터에 안전하게 들어갈 수 있도록 이스케이프
  return value.replace(/(["\\])/g, '\\$1');
}
