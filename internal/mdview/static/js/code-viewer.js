const MAX_HIGHLIGHT_CHARS = 500000;
const DEFAULT_LANGUAGE = 'plaintext';
const MARKDOWN_EXTS = ['.md', '.markdown', '.mdown', '.mkd'];
const ENV_FILE_NAME = '.env';
const ENV_FILE_PREFIX = '.env.';

// Must stay in sync with the server whitelist (internal/mdview/filetypes.go).
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
  txt: 'plaintext',
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

// Returns whether the code viewer knows the file's language (i.e. treats it as code/config).
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
 * Renders a source/config file as a code viewer with line numbers.
 * @param {string} text  Original file text
 * @param {string} path  File path (used for language detection)
 * @param {HTMLElement} container  Element to render into
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
