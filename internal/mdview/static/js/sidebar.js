const API_TREE_PATH = '/api/tree';

const tree = document.getElementById('tree');

let filePaths = new Set();

function collectFilePaths(node, paths) {
  if (!node.isDir) {
    paths.add(node.path);
    return;
  }
  (node.children || []).forEach((child) => collectFilePaths(child, paths));
}

// 트리 데이터를 DOM으로 변환한다.
function buildNodeElement(node, onSelect) {
  if (node.isDir) {
    const details = document.createElement('details');
    details.className = 'tree-dir';
    // 루트는 기본 펼침, 나머지는 접힘
    if (node.path === '') details.open = true;

    const summary = document.createElement('summary');
    summary.className = 'tree-dir-label';
    summary.textContent = node.name + '/';
    details.appendChild(summary);

    const list = document.createElement('ul');
    list.className = 'tree-list';
    (node.children || []).forEach((child) => {
      const li = document.createElement('li');
      li.appendChild(buildNodeElement(child, onSelect));
      list.appendChild(li);
    });
    details.appendChild(list);
    return details;
  }

  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'tree-file';
  btn.textContent = node.name;
  btn.dataset.path = node.path;
  btn.addEventListener('click', () => {
    onSelect(node.path);
  });
  return btn;
}

export async function initSidebar({ onSelect }) {
  const res = await fetch(API_TREE_PATH);
  if (!res.ok) {
    tree.innerHTML = '<div class="tree-error">트리를 불러오지 못했습니다.</div>';
    return;
  }
  const root = await res.json();
  filePaths = new Set();
  collectFilePaths(root, filePaths);
  tree.innerHTML = '';
  tree.appendChild(buildNodeElement(root, onSelect));
}

// 주어진 상대 경로가 트리에 존재하는 파일인지 반환한다.
export function hasFile(relPath) {
  return filePaths.has(relPath);
}

/**
 * 주어진 상대 경로에 해당하는 파일 버튼을 활성화하고,
 * 그 부모 디렉토리들을 모두 펼쳐서 화면에 보이도록 한다.
 */
export function highlightFile(relPath) {
  tree.querySelectorAll('.tree-file.active').forEach((el) => el.classList.remove('active'));
  if (!relPath) return;
  const btn = tree.querySelector(`.tree-file[data-path="${cssEscapeAttr(relPath)}"]`);
  if (!btn) return;
  btn.classList.add('active');
  // 조상 <details>를 모두 펼친다
  let el = btn.parentElement;
  while (el && el !== tree) {
    if (el.tagName === 'DETAILS') el.open = true;
    el = el.parentElement;
  }
  // 필요 시 스크롤
  btn.scrollIntoView({ block: 'nearest' });
}

function cssEscapeAttr(value) {
  // 속성 셀렉터에 안전하게 들어갈 수 있도록 이스케이프
  return value.replace(/(["\\])/g, '\\$1');
}
