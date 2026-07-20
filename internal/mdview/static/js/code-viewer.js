const MAX_HIGHLIGHT_CHARS = 500000;
const DEFAULT_LANGUAGE = 'plaintext';
const MARKDOWN_EXTS = ['.md', '.markdown', '.mdown', '.mkd'];
const ENV_FILE_NAME = '.env';
const ENV_FILE_PREFIX = '.env.';

// 서버 화이트리스트(internal/mdview/filetypes.go)와 동기화를 유지해야 한다.
const EXT_LANGUAGES = {
  go: 'go',
  js: 'javascript',
  mjs: 'javascript',
  cjs: 'javascript',
  jsx: 'javascript',
  ts: 'typescript',
  tsx: 'typescript',
  py: 'python',
  java: 'java',
  kt: 'kotlin',
  kts: 'kotlin',
  c: 'c',
  h: 'c',
  cc: 'cpp',
  cpp: 'cpp',
  hpp: 'cpp',
  cs: 'csharp',
  rs: 'rust',
  rb: 'ruby',
  php: 'php',
  swift: 'swift',
  sh: 'bash',
  bash: 'bash',
  zsh: 'bash',
  fish: 'bash',
  sql: 'sql',
  lua: 'lua',
  r: 'r',
  pl: 'perl',
  scala: 'scala',
  dart: 'dart',
  vue: 'xml',
  svelte: 'xml',
  html: 'xml',
  htm: 'xml',
  xml: 'xml',
  css: 'css',
  scss: 'scss',
  less: 'less',
  json: 'json',
  yaml: 'yaml',
  yml: 'yaml',
  toml: 'ini',
  ini: 'ini',
  env: 'ini',
  conf: 'ini',
  cfg: 'ini',
  properties: 'ini',
  gradle: 'gradle',
  mod: 'plaintext',
  sum: 'plaintext',
  lock: 'plaintext',
};

const NAME_LANGUAGES = {
  dockerfile: 'dockerfile',
  makefile: 'makefile',
  '.gitignore': 'ini',
  '.dockerignore': 'ini',
  '.gitattributes': 'ini',
  '.editorconfig': 'ini',
};

export function isMarkdownPath(path) {
  const lower = path.toLowerCase();
  return MARKDOWN_EXTS.some((ext) => lower.endsWith(ext));
}

// 코드뷰어가 언어를 알고 있는(코드/설정으로 취급할) 파일인지 반환한다.
export function isCodeViewerPath(path) {
  const name = baseName(path);
  if (NAME_LANGUAGES[name]) return true;
  if (name === ENV_FILE_NAME || name.startsWith(ENV_FILE_PREFIX)) return true;
  const dotIndex = name.lastIndexOf('.');
  if (dotIndex <= 0) return false;
  return EXT_LANGUAGES[name.slice(dotIndex + 1)] !== undefined;
}

function baseName(path) {
  return path.split(/[\\/]/).pop().toLowerCase();
}

function detectLanguage(path) {
  const name = baseName(path);
  if (NAME_LANGUAGES[name]) return NAME_LANGUAGES[name];
  if (name === ENV_FILE_NAME || name.startsWith(ENV_FILE_PREFIX)) return 'ini';
  const dotIndex = name.lastIndexOf('.');
  if (dotIndex <= 0) return DEFAULT_LANGUAGE;
  return EXT_LANGUAGES[name.slice(dotIndex + 1)] || DEFAULT_LANGUAGE;
}

function escapeHtml(text) {
  return text.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

function normalizeContent(text) {
  const unified = text.replace(/\r\n?/g, '\n');
  return unified.endsWith('\n') ? unified.slice(0, -1) : unified;
}

function highlightCode(code, language) {
  const hljs = window.hljs;
  const canHighlight =
    hljs &&
    language !== DEFAULT_LANGUAGE &&
    hljs.getLanguage &&
    hljs.getLanguage(language) &&
    code.length <= MAX_HIGHLIGHT_CHARS;
  if (!canHighlight) {
    return { html: escapeHtml(code), language: DEFAULT_LANGUAGE };
  }
  try {
    return { html: hljs.highlight(code, { language }).value, language };
  } catch {
    return { html: escapeHtml(code), language: DEFAULT_LANGUAGE };
  }
}

/**
 * 소스코드/설정 파일을 라인 번호가 있는 코드뷰어 형태로 렌더링한다.
 * @param {string} text  파일 원본 텍스트
 * @param {string} path  파일 경로 (언어 감지에 사용)
 * @param {HTMLElement} container  렌더링 대상 요소
 */
export function renderCode(text, path, container) {
  const code = normalizeContent(text);
  const { html, language } = highlightCode(code, detectLanguage(path));
  const lineCount = code === '' ? 1 : code.split('\n').length;
  const gutter = Array.from({ length: lineCount }, (_, i) => i + 1).join('\n');
  container.innerHTML =
    `<div class="code-viewer" data-line-count="${lineCount}">` +
    `<pre class="code-gutter" aria-hidden="true">${gutter}</pre>` +
    `<pre class="code-content"><code class="hljs language-${language}">${html}</code></pre>` +
    '</div>';
}
