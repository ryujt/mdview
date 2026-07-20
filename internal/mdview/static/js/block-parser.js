export const BlockType = {
  HEADING: 'heading',
  PARAGRAPH: 'paragraph',
  CODE_FENCE: 'code_fence',
  LIST: 'list',
  BLOCKQUOTE: 'blockquote',
  HORIZONTAL_RULE: 'hr',
  TABLE: 'table',
  EMPTY: 'empty'
};

// ```text 펜스는 "안의 코드 예시를 통째로 보여주는" 용도라서, 내부의
// ```xxx(정보 문자열 있는 펜스)를 중첩 열림으로 세고 맨몸 ```를 닫힘으로 빼서
// 짝이 맞아떨어지는 닫는 펜스까지를 하나의 영역으로 취급한다.
export function isTextFenceOpener(line) {
  return /^(`{3,}|~{3,})[ \t]*text[ \t]*$/i.test(line);
}

// lines[openIndex]가 text 펜스 여는 줄일 때 닫는 줄의 인덱스를 반환한다 (미종결이면 -1).
export function findTextFenceClose(lines, openIndex) {
  let depth = 0;
  for (let i = openIndex + 1; i < lines.length; i++) {
    const m = lines[i].match(/^(`{3,}|~{3,})[ \t]*(\S*)/);
    if (!m) continue;
    if (m[2]) depth++;
    else if (depth > 0) depth--;
    else return i;
  }
  return -1;
}

export function generateBlockHash(content) {
  let hash = 5381;
  for (let i = 0; i < content.length; i++) {
    hash = ((hash << 5) + hash) + content.charCodeAt(i);
    hash = hash & hash;
  }
  return Math.abs(hash).toString(36);
}

export function parseMarkdownToBlocks(markdown) {
  const lines = markdown.split('\n');
  const blocks = [];
  let currentBlock = null;
  let inCodeFence = false;
  let codeFenceLanguage = '';
  let inList = false;
  let inBlockquote = false;
  let inTable = false;

  function finalizeBlock() {
    if (currentBlock && currentBlock.lines.length > 0) {
      const content = currentBlock.lines.join('\n');
      blocks.push({
        type: currentBlock.type,
        content,
        hash: generateBlockHash(content),
        startLine: currentBlock.startLine,
        endLine: currentBlock.startLine + currentBlock.lines.length - 1,
        language: currentBlock.language || null
      });
    }
    currentBlock = null;
  }

  function startBlock(type, lineNumber, language = null) {
    finalizeBlock();
    currentBlock = { type, lines: [], startLine: lineNumber, language };
  }

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    const lineNumber = i + 1;
    const codeFenceMatch = line.match(/^(`{3,}|~{3,})(\w*)?/);

    if (codeFenceMatch) {
      if (!inCodeFence) {
        inCodeFence = true;
        codeFenceLanguage = codeFenceMatch[2] || '';
        inList = false; inBlockquote = false; inTable = false;
        startBlock(BlockType.CODE_FENCE, lineNumber, codeFenceLanguage);
        currentBlock.lines.push(line);
        if (isTextFenceOpener(line)) {
          const close = findTextFenceClose(lines, i);
          const end = close === -1 ? lines.length - 1 : close;
          for (let j = i + 1; j <= end; j++) currentBlock.lines.push(lines[j]);
          i = end;
          inCodeFence = false;
          codeFenceLanguage = '';
          finalizeBlock();
        }
      } else {
        currentBlock.lines.push(line);
        inCodeFence = false;
        codeFenceLanguage = '';
        finalizeBlock();
      }
      continue;
    }

    if (inCodeFence) { currentBlock.lines.push(line); continue; }

    if (line.trim() === '') {
      if (inList || inBlockquote || inTable) {
        finalizeBlock();
        inList = false; inBlockquote = false; inTable = false;
      } else if (currentBlock && currentBlock.type === BlockType.PARAGRAPH) {
        finalizeBlock();
      }
      if (!currentBlock) startBlock(BlockType.EMPTY, lineNumber);
      if (currentBlock) currentBlock.lines.push(line);
      continue;
    }

    if (/^#{1,6}\s/.test(line)) {
      inList = false; inBlockquote = false; inTable = false;
      startBlock(BlockType.HEADING, lineNumber);
      currentBlock.lines.push(line);
      finalizeBlock();
      continue;
    }

    if (/^(\*{3,}|-{3,}|_{3,})$/.test(line.trim())) {
      inList = false; inBlockquote = false; inTable = false;
      startBlock(BlockType.HORIZONTAL_RULE, lineNumber);
      currentBlock.lines.push(line);
      finalizeBlock();
      continue;
    }

    if (/^\|.*\|/.test(line) || /^[-:|]+$/.test(line.trim())) {
      if (!inTable) { inList = false; inBlockquote = false; inTable = true; startBlock(BlockType.TABLE, lineNumber); }
      currentBlock.lines.push(line);
      continue;
    } else if (inTable) { inTable = false; finalizeBlock(); }

    if (/^(\s*[-*+]|\s*\d+\.)\s/.test(line)) {
      if (!inList) { inBlockquote = false; inTable = false; inList = true; startBlock(BlockType.LIST, lineNumber); }
      currentBlock.lines.push(line);
      continue;
    } else if (inList && /^\s+/.test(line)) {
      currentBlock.lines.push(line);
      continue;
    } else if (inList) { inList = false; finalizeBlock(); }

    if (/^>\s?/.test(line)) {
      if (!inBlockquote) { inList = false; inTable = false; inBlockquote = true; startBlock(BlockType.BLOCKQUOTE, lineNumber); }
      currentBlock.lines.push(line);
      continue;
    } else if (inBlockquote) { inBlockquote = false; finalizeBlock(); }

    if (!currentBlock || currentBlock.type !== BlockType.PARAGRAPH) {
      startBlock(BlockType.PARAGRAPH, lineNumber);
    }
    currentBlock.lines.push(line);
  }

  finalizeBlock();
  return blocks;
}
