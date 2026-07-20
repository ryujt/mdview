export function stateToMermaid(input) {
  let mermaidCode = "graph LR\n";
  const stateNodeMap = {};
  let stateNodeCounter = 65;
  const nodeStyles = [];
  const links = [];
  const nodeDefinitions = {};

  const getNodeIdentifier = (nodeName) => {
    let processedName = nodeName.trim();
    let isStart = false;
    let isEnd = false;
    let isEvent = false;
    let isRounded = false;

    if (processedName.toLowerCase() === '<s>') {
      isStart = true;
      processedName = 'Start';
    } else if (processedName.toLowerCase() === '<e>') {
      isEnd = true;
      processedName = 'End';
    } else if (processedName === '()') {
      isEnd = true;
      processedName = 'Start';
    } else if (processedName === '.') {
      isEnd = true;
      processedName = 'End';
    } else if (processedName.startsWith('<') && processedName.endsWith('>')) {
      processedName = processedName.substring(1, processedName.length - 1);
      isEvent = true;
    } else if (processedName.startsWith('(') && processedName.endsWith(')')) {
      processedName = processedName.substring(1, processedName.length - 1);
      isRounded = true;
    }

    let identifier = stateNodeMap[processedName];
    if (!identifier) {
      identifier = String.fromCharCode(stateNodeCounter++);
      stateNodeMap[processedName] = identifier;

      const escapedName = processedName
        .replace(/"/g, '#quot;')
        .replace(/@/g, '#64;')
        .replace(/\(/g, '#40;')
        .replace(/\)/g, '#41;')
        .replace(/\[/g, '#91;')
        .replace(/\]/g, '#93;')
        .replace(/\{/g, '#123;')
        .replace(/\}/g, '#125;');

      let nodeShape;
      if (isStart) {
        nodeShape = `(( ))`;
        nodeStyles.push(`    style ${identifier} fill:#fff,stroke:#000,stroke-width:2px`);
      } else if (isEnd) {
        nodeShape = `(( ))`;
        nodeStyles.push(`    style ${identifier} fill:#000,stroke:#000,stroke-width:2px`);
      } else if (isEvent) {
        nodeShape = `{{"${escapedName}"}}`;
        nodeStyles.push(`    style ${identifier} fill:#dfd,stroke:#333,stroke-width:2px`);
      } else if (isRounded) {
        nodeShape = `("${escapedName}")`;
      } else {
        nodeShape = `["${escapedName}"]`;
      }

      nodeDefinitions[identifier] = `    ${identifier}${nodeShape}`;
    }
    return identifier;
  };

  const lines = input.split('\n');
  lines.forEach(line => {
    line = line.trim();
    if (line === '') return;

    const arrowIndex = line.indexOf('-->');
    if (arrowIndex === -1) return;

    const beforeArrow = line.substring(0, arrowIndex).trim();
    const afterArrow = line.substring(arrowIndex + 3).trim();
    const fromNode = beforeArrow;

    const lastColonIndex = afterArrow.lastIndexOf(' : ');
    let toNode, label;

    if (lastColonIndex !== -1) {
      toNode = afterArrow.substring(0, lastColonIndex).trim();
      label = afterArrow.substring(lastColonIndex + 3).trim();
    } else {
      toNode = afterArrow;
      label = '';
    }

    const fromId = getNodeIdentifier(fromNode);
    const toId = getNodeIdentifier(toNode);

    if (label) {
      const escapedLabel = label.replace(/"/g, '#quot;').replace(/@/g, '#64;').replace(/\(/g, '#40;').replace(/\)/g, '#41;');
      links.push(`    ${fromId} -->|"${escapedLabel}"| ${toId}`);
    } else {
      links.push(`    ${fromId} --> ${toId}`);
    }
  });

  mermaidCode += Object.values(nodeDefinitions).join('\n') + '\n\n';
  mermaidCode += links.join('\n') + '\n';

  if (nodeStyles.length > 0) {
    mermaidCode += '\n' + nodeStyles.join('\n');
  }

  return mermaidCode;
}
