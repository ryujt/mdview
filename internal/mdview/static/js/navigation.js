export function navigationToMermaid(input) {
  let mermaidCode = "graph LR\n";
  const nodeMap = {};
  const nodeStyles = [];
  let nodeCounter = 0;
  const links = [];
  const nodeDefinitions = {};

  const getNodeIdentifier = (nodeName) => {
    let processedName = nodeName;
    let isRounded = false;
    let isEvent = false;
    let isMessage = false;

    if (processedName.length >= 2 && processedName.startsWith('`') && processedName.endsWith('`')) {
      processedName = processedName.substring(1, processedName.length - 1);
      isMessage = true;
    } else if (processedName.startsWith('<') && processedName.endsWith('>')) {
      processedName = processedName.substring(1, processedName.length - 1);
      isEvent = true;
    } else if (processedName.startsWith('(') && processedName.endsWith(')')) {
      processedName = processedName.substring(1, processedName.length - 1);
      isRounded = true;
    } else if (processedName.startsWith('/')) {
      processedName = processedName.substring(1);
      isRounded = true;
    }

    let identifier = nodeMap[processedName];
    if (!identifier) {
      identifier = `N${nodeCounter++}`;
      nodeMap[processedName] = identifier;

      const escapedName = processedName
        .replace(/"/g, '#quot;')
        .replace(/@/g, '#64;')
        .replace(/\(/g, '#40;')
        .replace(/\)/g, '#41;')
        .replace(/\[/g, '#91;')
        .replace(/\]/g, '#93;')
        .replace(/\{/g, '#123;')
        .replace(/\}/g, '#125;');
      let nodeDef;
      if (isMessage) {
        nodeDef = `    ${identifier}@{ shape: fr-rect, label: "${escapedName}" }`;
      } else if (isEvent) {
        nodeDef = `    ${identifier}{{"${escapedName}"}}`;
      } else if (isRounded) {
        nodeDef = `    ${identifier}("${escapedName}")`;
      } else {
        nodeDef = `    ${identifier}["${escapedName}"]`;
      }

      nodeDefinitions[identifier] = nodeDef;

      if (isMessage) {
        nodeStyles.push(`    style ${identifier} fill:#fff7d6,stroke:#333,stroke-width:2px`);
      } else if (isEvent) {
        nodeStyles.push(`    style ${identifier} fill:#dfd,stroke:#333,stroke-width:2px`);
      } else if (!isRounded) {
        nodeStyles.push(`    style ${identifier} fill:#bbf,stroke:#333,stroke-width:2px`);
      }
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
