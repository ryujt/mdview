// sidebar.js — file tree sidebar Worker
//
// Instead of loading the whole tree at once, each time a folder is expanded only that
// directory's first-level listing is requested via /api/tree?path=. Re-expanding a folder refreshes it.

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
    // When opened from code (openLoaded) the listing is already fresh, so do not refetch.
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

// After refilling the listing, reload and re-expand subfolders that were expanded before the refresh.
function restoreOpenDirs(list, openPaths) {
  if (!openPaths || openPaths.size === 0) return;
  list.querySelectorAll(':scope > li > details.tree-dir').forEach((dir) => {
    if (!openPaths.has(dir.dataset.path)) return;
    loadChildren(dir, openPaths).then(() => openLoaded(dir));
  });
}

// Remember which paths under this folder were expanded before the refresh removes them.
function collectOpenPaths(details) {
  return new Set(
    [...details.querySelectorAll('details.tree-dir[open]')].map((d) => d.dataset.path)
  );
}

// Concurrent requests for the same folder are merged into the single in-flight fetch.
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
  msg.textContent = 'Failed to load the listing.';
  li.appendChild(msg);
  list.replaceChildren(li);
}

// Expands <details> without the toggle handler refetching (when the listing is already filled).
function openLoaded(details) {
  if (details.open) return;
  details._suppressNextToggle = true;
  details.open = true;
}

// If closed, load the listing and expand; if already open, just wait for loading to finish.
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
    tree.innerHTML = '<div class="tree-error">Failed to load the tree.</div>';
    return;
  }
  rootDetails = buildDirElement(root);
  populate(rootDetails, root);
  openLoaded(rootDetails);
  tree.replaceChildren(rootDetails);
}

/** Updates the active-file highlight. An empty value clears all highlights. */
export function highlightFile(relPath) {
  activePath = relPath || '';
  applyActiveHighlight();
}

/**
 * Loads/expands ancestor folders in order, then marks the file as active.
 * In the lazy tree, ancestors of a file opened via a deep link or cross-document link
 * may not be loaded yet, so use this function instead of highlightFile.
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
    return; // If the tree fails to load, just skip the highlight
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
  // Escape so it can be safely placed in an attribute selector
  return value.replace(/(["\\])/g, '\\$1');
}
