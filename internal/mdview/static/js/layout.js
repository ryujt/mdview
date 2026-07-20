const COMPONENT_HEIGHT = 48;
const GAP = 8;
const PADDING = 8;

function parseChild(childStr) {
  const parts = childStr.split(':').map(p => p.trim());
  const name = parts[0];
  const parsed = parts.length > 1 ? parseInt(parts[1], 10) : NaN;
  const size = Number.isFinite(parsed) ? parsed : null;
  return { name, size };
}

function parseLayoutDSL(dsl) {
  const lines = dsl.trim().split('\n').filter(line => line.trim());
  const containers = new Map();
  let rootName = null;

  for (const line of lines) {
    const trimmed = line.trim();
    if (!trimmed) continue;

    let direction, containerName, childrenStr;

    if (trimmed.includes(' V ')) {
      const parts = trimmed.split(' V ');
      containerName = parts[0].trim();
      childrenStr = parts[1];
      direction = 'column';
    } else if (trimmed.includes(' > ')) {
      const parts = trimmed.split(' > ');
      containerName = parts[0].trim();
      childrenStr = parts[1];
      direction = 'row';
    } else {
      continue;
    }

    const childrenRaw = childrenStr.split(',').map(c => c.trim()).filter(c => c);
    const children = childrenRaw.map(parseChild);

    const totalSpecified = children.reduce((sum, c) => sum + (c.size || 0), 0);
    const unspecifiedCount = children.filter(c => c.size === null).length;
    const remainingSpace = Math.max(0, 100 - totalSpecified);
    const defaultSize = unspecifiedCount > 0 ? remainingSpace / unspecifiedCount : 0;

    children.forEach(c => {
      if (c.size === null) c.size = defaultSize;
    });

    containers.set(containerName, { direction, children });
    if (rootName === null) rootName = containerName;
  }

  return { containers, rootName };
}

function getVerticalWeight(name, containers) {
  const info = containers.get(name);
  if (!info) return 1;
  const { direction, children } = info;
  if (children.length === 0) return 1;
  if (direction === 'column') {
    return children.reduce((sum, child) => sum + getVerticalWeight(child.name, containers), 0);
  } else {
    return Math.max(...children.map(child => getVerticalWeight(child.name, containers)));
  }
}

function toId(name) {
  // 'lyt-' 접두사: 호스트 페이지의 전역 CSS id 규칙(#sidebar, #content 등)과의 충돌 방지
  return 'lyt-' + name.split(' ').map((word, index) =>
    index === 0 ? word.toLowerCase() : word.charAt(0).toUpperCase() + word.slice(1).toLowerCase()
  ).join('');
}

function buildHTML(containerName, containers, depth = 0, size = null, parentDirection = null) {
  const indent = '  '.repeat(depth);
  const id = toId(containerName);
  const info = containers.get(containerName);

  if (!info) {
    const styles = [];
    if (parentDirection === 'row' && size !== null) styles.push(`flex: ${size} 0 0%`);
    if (parentDirection === 'column') styles.push(`flex: 1 0 auto`);
    return `${indent}<div id="${id}" class="layout-component"${styles.length ? ` style="${styles.join('; ')}"` : ''}>${containerName}</div>`;
  }

  const { direction, children } = info;
  const dirClass = direction === 'column' ? 'layout-vertical' : 'layout-horizontal';

  const styles = [];
  if (parentDirection === 'row' && size !== null) styles.push(`flex: ${size} 0 0%`);
  if (parentDirection === 'column') {
    const weight = getVerticalWeight(containerName, containers);
    styles.push(`flex: ${weight} 0 auto`);
  }

  const styleAttr = styles.length > 0 ? ` style="${styles.join('; ')}"` : '';
  let html = `${indent}<div id="${id}" class="layout-container ${dirClass}"${styleAttr}>\n`;

  for (const child of children) {
    if (containers.has(child.name)) {
      html += buildHTML(child.name, containers, depth + 1, child.size, direction) + '\n';
    } else {
      const childId = toId(child.name);
      const childStyles = [];
      if (direction === 'row' && child.size !== null) childStyles.push(`flex: ${child.size} 0 0%`);
      if (direction === 'column') childStyles.push(`flex: 1 0 auto`);
      html += `${indent}  <div id="${childId}" class="layout-component"${childStyles.length ? ` style="${childStyles.join('; ')}"` : ''}>${child.name}</div>\n`;
    }
  }

  html += `${indent}</div>`;
  return html;
}

export function generateHTML(dsl) {
  const { containers, rootName } = parseLayoutDSL(dsl);
  if (!rootName) return '<div class="layout-error">Invalid layout definition</div>';
  const structureHTML = buildHTML(rootName, containers);
  return `<div class="layout-preview">${structureHTML}</div>`;
}

export function generateStyle() {
  return `
    .layout-preview { font-family: system-ui, -apple-system, sans-serif; font-size: 12px; background: #f8f9fa; padding: 16px; border-radius: 8px; }
    .layout-container { display: flex; gap: ${GAP}px; padding: ${PADDING}px; border: 1px solid #dee2e6; border-radius: 4px; background: white; box-sizing: border-box; overflow: hidden; }
    .layout-vertical { flex-direction: column; }
    .layout-horizontal { flex-direction: row; align-items: stretch; }
    .layout-component { display: flex; align-items: center; justify-content: center; min-height: ${COMPONENT_HEIGHT}px; padding: 8px 12px; border: 1px dashed #adb5bd; border-radius: 4px; background: #e9ecef; color: #495057; text-align: center; white-space: nowrap; box-sizing: border-box; }
    .layout-horizontal > .layout-container, .layout-horizontal > .layout-component { flex: 1 0 0%; }
    .layout-error { color: #dc3545; padding: 16px; text-align: center; }
  `;
}

const LayoutToHTML = { generateHTML, generateStyle, parseLayoutDSL, toId };
export default LayoutToHTML;
