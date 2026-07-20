const JobFlowToSVG = {
  parseJobFlow(input) {
    input = this.addAsterisksToInput(input);
    const lines = input.replaceAll(".result.", ".data.").split('\n');
    const objects = [];
    const objectSet = new Set();
    const relationships = [];
    const standaloneShapes = [];
    let masterObject = '';
    let lineIndex = 0;
    const addObject = (objectName) => {
      const trimmed = objectName.trim();
      if (trimmed && !objectSet.has(trimmed)) {
        objectSet.add(trimmed);
        objects.push(trimmed);
      }
    };
    lines.forEach(line => {
      if (line.toLowerCase().startsWith("master:")) {
        masterObject = line.split(":")[1].trim();
      } else if (line.toLowerCase().startsWith("object:")) {
        const objectNames = line.split(":")[1].split(',').map(obj => obj.trim()).filter(Boolean);
        objectNames.forEach(addObject);
      } else if (line.includes('-->')) {
        const [from, to] = line.split('-->').map(part => part.trim());
        const [fromObject, fromAction] = this.parseAction(from);
        const [toObject, toAction] = this.parseAction(to);
        addObject(fromObject);
        addObject(toObject);
        relationships.push({ from: fromObject, fromAction, to: toObject, toAction, lineIndex });
      } else if (line.trim()) {
        const trimmedLine = line.trim();
        if (trimmedLine.includes('.')) {
          const [obj, action] = this.parseAction(trimmedLine);
          const trimmedObj = obj.trim();
          if (trimmedObj) {
            addObject(trimmedObj);
            standaloneShapes.push({ object: trimmedObj, action, lineIndex });
          }
        }
      }
      lineIndex++;
    });
    return { masterObject, objects, relationships, standaloneShapes };
  },
  addAsterisksToInput(input) {
    const lines = input.split('\n');
    const wordCount = {};
    return lines.map(line => {
      if (line.includes('-->')) {
        const [left, right] = line.split('-->').map(part => part.trim());
        const leftParts = left.split('.');
        const rightParts = right.split('.');
        const leftLastWord = leftParts[leftParts.length - 1];
        const rightLastWord = rightParts[rightParts.length - 1];
        wordCount[leftLastWord] = (wordCount[leftLastWord] || 0) + 1;
        wordCount[rightLastWord] = (wordCount[rightLastWord] || 0) + 1;
        const leftAsterisks = '*'.repeat(wordCount[leftLastWord] - 1);
        const rightAsterisks = '*'.repeat(wordCount[rightLastWord] - 1);
        const newLeft = [...leftParts.slice(0, -1), leftLastWord + leftAsterisks].join('.');
        const newRight = [...rightParts.slice(0, -1), rightLastWord + rightAsterisks].join('.');
        return `${newLeft} --> ${newRight}`;
      }
      return line;
    }).join('\n');
  },
  parseAction(action) {
    const parts = action.split('.');
    return [parts[0], parts.slice(1).join('.')];
  },
  normalizeAction(action) {
    return action.replace(/\*+$/, '');
  },
  parentMethodKeyOf(key) {
    const parts = key.split('.');
    if (parts.length <= 2) return null;
    return parts.slice(0, -1).join('.');
  },
  escapeXml(text) {
    return String(text)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  },
  generateSVG(jobFlowScript) {
    const { objects, relationships, standaloneShapes } = this.parseJobFlow(jobFlowScript);
    const OBJECT_WIDTH = 240;
    const OBJECT_HEIGHT = 32;
    const VERTICAL_GAP = 50;
    const SHAPE_WIDTH = 180;
    const SHAPE_HEIGHT = 30;
    const SHAPE_LEFT_MARGIN = (OBJECT_WIDTH - SHAPE_WIDTH) / 2;
    const yBase = OBJECT_HEIGHT + VERTICAL_GAP;
    const svgWidth = objects.length * OBJECT_WIDTH;
    const colIndex = new Map(objects.map((o, i) => [o, i]));
    const grid = [];
    const rowEdges = [];
    const colEdges = Array(objects.length).fill(0).map(() => []);
    const shapesByCell = new Map();
    const edges = [];
    const lastPosByKey = new Map();
    const shapesByKey = new Map();
    const usedAsSource = new Set();
    let prevTo = null;
    const ensureRow = (r) => {
      while (grid.length <= r) {
        grid.push({ cells: Array(objects.length).fill(null) });
        rowEdges.push(Array(Math.max(0, objects.length - 1)).fill(false));
      }
    };
    const cellKey = (r, c) => `${r},${c}`;
    const placeShapeIfAbsent = (key, text, type, col, row) => {
      ensureRow(row);
      if (!grid[row].cells[col]) {
        grid[row].cells[col] = { key, text, type, col, row };
        shapesByCell.set(cellKey(row, col), grid[row].cells[col]);
      }
      return grid[row].cells[col];
    };
    const isHorizontalCrossing = (row, c1, c2) => {
      const a = Math.min(c1, c2);
      const b = Math.max(c1, c2);
      for (let c = a + 1; c < b; c++) if (grid[row] && grid[row].cells[c]) return true;
      for (let e = a; e < b; e++) if (rowEdges[row] && rowEdges[row][e]) return true;
      for (let c = a + 1; c < b; c++) if (row > 0 && colEdges[c] && colEdges[c][row - 1]) return true;
      return false;
    };
    const isVerticalCrossing = (col, r1, r2) => {
      const a = Math.min(r1, r2);
      const b = Math.max(r1, r2);
      for (let r = a + 1; r < b; r++) if (grid[r] && grid[r].cells[col]) return true;
      for (let e = a; e < b; e++) if (colEdges[col][e]) return true;
      return false;
    };
    const markHorizontal = (row, c1, c2) => {
      const a = Math.min(c1, c2);
      const b = Math.max(c1, c2);
      for (let e = a; e < b; e++) rowEdges[row][e] = true;
    };
    const markVertical = (col, r1, r2) => {
      const a = Math.min(r1, r2);
      const b = Math.max(r1, r2);
      for (let e = a; e < b; e++) colEdges[col][e] = true;
    };
    const allItems = [
      ...relationships.map(rel => ({ type: 'relationship', item: rel, lineIndex: rel.lineIndex })),
      ...standaloneShapes.map(standalone => ({ type: 'standalone', item: standalone, lineIndex: standalone.lineIndex }))
    ].sort((a, b) => a.lineIndex - b.lineIndex);

    const bentArrowCount = new Map();
    allItems.forEach(({ type, item }) => {
      if (type === 'relationship') {
        const rel = item;
        const fromCol = colIndex.get(rel.from);
        const toCol = colIndex.get(rel.to);
        const fromKey = `${rel.from}.${this.normalizeAction(rel.fromAction)}`;
        const toKey = `${rel.to}.${this.normalizeAction(rel.toAction)}`;
        const fromText = this.getDisplayText(rel.fromAction);
        const toText = this.getDisplayText(rel.toAction);
        const fromType = this.getShapeType(rel.fromAction);
        const toType = this.getShapeType(rel.toAction);
        let canUseBentArrow = false;
        let existingFromShape = null;
        if (shapesByKey.has(fromKey) && usedAsSource.has(fromKey) && fromCol !== toCol) {
          existingFromShape = shapesByKey.get(fromKey);
          let hasInterveningShapes = false;
          for (let r = existingFromShape.row + 1; r < grid.length; r++) {
            if (grid[r] && grid[r].cells[fromCol]) { hasInterveningShapes = true; break; }
          }
          canUseBentArrow = !hasInterveningShapes;
        }
        if (canUseBentArrow && existingFromShape) {
          let toRow = existingFromShape.row + 1;
          ensureRow(toRow);
          const checkHorizontalPathClear = (row) => {
            if (grid[row] && grid[row].cells[toCol]) return false;
            if (row > 0 && colEdges[toCol] && colEdges[toCol][row - 1]) return false;
            const minCol = Math.min(fromCol, toCol);
            const maxCol = Math.max(fromCol, toCol);
            for (let c = minCol + 1; c < maxCol; c++) {
              if (grid[row] && grid[row].cells[c]) return false;
              if (row > 0 && colEdges[c] && colEdges[c][row - 1]) return false;
            }
            for (let c = minCol; c < maxCol; c++) { if (rowEdges[row] && rowEdges[row][c]) return false; }
            return true;
          };
          while (!checkHorizontalPathClear(toRow)) { toRow++; ensureRow(toRow); }
          const toShape = toType !== "" ? placeShapeIfAbsent(toKey, toText, toType, toCol, toRow) : { col: toCol, row: toRow, dummy: true };
          if (toType !== "") shapesByKey.set(toKey, toShape);
          const bentCount = bentArrowCount.get(fromKey) || 0;
          bentArrowCount.set(fromKey, bentCount + 1);
          edges.push({ from: existingFromShape, to: toShape, bent: true, bentIndex: bentCount });
          usedAsSource.add(fromKey);
          markHorizontal(toRow, fromCol, toCol);
          if (toRow > existingFromShape.row) markVertical(fromCol, existingFromShape.row, toRow);
          if (toType !== "") lastPosByKey.set(toKey, { row: toRow, col: toCol });
          prevTo = toType !== "" ? { key: toKey, row: toRow, col: toCol } : null;
        } else {
          let fromRow;
          let fromShape;
          const existingShape = shapesByKey.get(fromKey);
          let canReuseShape = false;
          if (existingShape) {
            let hasInterveningShapes = false;
            for (let r = existingShape.row + 1; r < grid.length; r++) {
              if (grid[r] && grid[r].cells[fromCol]) { hasInterveningShapes = true; break; }
            }
            canReuseShape = !hasInterveningShapes;
          }
          if (existingShape && canReuseShape) {
            fromRow = existingShape.row;
            fromShape = existingShape;
            let toRow = rel.from === rel.to ? fromRow + 1 : fromRow;
            if (fromCol === toCol) {
              toRow = grid.length > fromRow ? grid.length : fromRow + 1;
              ensureRow(toRow);
              while (grid[toRow] && grid[toRow].cells[toCol]) { toRow++; ensureRow(toRow); }
            } else {
              const checkPathClear = (row) => {
                if (grid[row] && grid[row].cells[toCol]) return false;
                if (row > 0 && colEdges[toCol] && colEdges[toCol][row - 1]) return false;
                const minCol = Math.min(fromCol, toCol);
                const maxCol = Math.max(fromCol, toCol);
                for (let c = minCol + 1; c < maxCol; c++) {
                  if (grid[row] && grid[row].cells[c]) return false;
                  if (row > 0 && colEdges[c] && colEdges[c][row - 1]) return false;
                }
                for (let c = minCol; c < maxCol; c++) { if (rowEdges[row] && rowEdges[row][c]) return false; }
                return true;
              };
              ensureRow(toRow);
              while (!checkPathClear(toRow)) { toRow++; ensureRow(toRow); }
            }
            const toShape = toType !== "" ? placeShapeIfAbsent(toKey, toText, toType, toCol, toRow) : { col: toCol, row: toRow, dummy: true };
            if (toType !== "") shapesByKey.set(toKey, toShape);
            edges.push({ from: fromShape, to: toShape, bent: false });
            usedAsSource.add(fromKey);
            if (fromCol === toCol) {
              markVertical(fromCol, fromRow, toRow);
            } else {
              markHorizontal(toRow, fromCol, toCol);
              if (toRow > fromRow) markVertical(fromCol, fromRow, toRow);
            }
            if (toType !== "") lastPosByKey.set(toKey, { row: toRow, col: toCol });
            prevTo = toType !== "" ? { key: toKey, row: toRow, col: toCol } : null;
          } else {
            if (prevTo && prevTo.key === fromKey) {
              fromRow = prevTo.row;
            } else if (lastPosByKey.has(fromKey)) {
              const p = lastPosByKey.get(fromKey);
              if ((p.row === grid.length - 1 && p.col === fromCol) || (p.row === grid.length && p.col === fromCol - 1) || (p.row === grid.length && p.col === fromCol + 1)) {
                fromRow = p.row;
              } else { fromRow = grid.length; }
            } else { fromRow = grid.length; }
            let toRow = rel.from === rel.to ? fromRow + 1 : fromRow;
            let crossing = true;
            let triedReuse = fromRow < grid.length;
            while (crossing) {
              ensureRow(fromRow);
              if (rel.from === rel.to) ensureRow(toRow);
              if (fromCol === toCol) { crossing = isVerticalCrossing(fromCol, fromRow, toRow); }
              else { crossing = isHorizontalCrossing(fromRow, fromCol, toCol); }
              if (crossing) { fromRow = triedReuse ? grid.length : fromRow + 1; triedReuse = false; toRow = rel.from === rel.to ? fromRow + 1 : fromRow; }
            }
            fromShape = placeShapeIfAbsent(fromKey, fromText, fromType, fromCol, fromRow);
            shapesByKey.set(fromKey, fromShape);
            const toShape = toType !== "" ? placeShapeIfAbsent(toKey, toText, toType, toCol, toRow) : { col: toCol, row: toRow, dummy: true };
            if (toType !== "") shapesByKey.set(toKey, toShape);
            edges.push({ from: fromShape, to: toShape, bent: false });
            usedAsSource.add(fromKey);
            if (fromCol === toCol) markVertical(fromCol, fromRow, toRow); else markHorizontal(fromRow, fromCol, toCol);
            if (toType !== "") lastPosByKey.set(toKey, { row: toRow, col: toCol });
            prevTo = toType !== "" ? { key: toKey, row: toRow, col: toCol } : null;
          }
        }
      } else if (type === 'standalone') {
        const standalone = item;
        const col = colIndex.get(standalone.object);
        const key = `${standalone.object}.${this.normalizeAction(standalone.action)}`;
        const text = this.getDisplayText(standalone.action);
        const shapeType = this.getShapeType(standalone.action);
        let row = grid.length;
        placeShapeIfAbsent(key, text, shapeType, col, row);
        lastPosByKey.set(key, { row, col });
        prevTo = { key, row, col };
      }
    });

    const svgHeight = yBase + grid.length * VERTICAL_GAP + VERTICAL_GAP;
    const objectHeader = {};
    objects.forEach((o, i) => {
      objectHeader[o] = { x: i * OBJECT_WIDTH, y: 0, width: OBJECT_WIDTH, height: OBJECT_HEIGHT };
    });
    let svg = `<svg xmlns="http://www.w3.org/2000/svg" width="${svgWidth}" height="${svgHeight}" viewBox="0 0 ${svgWidth} ${svgHeight}">`;
    objects.forEach(o => {
      const s = objectHeader[o];
      const headerFontSize = this.calculateFontSize(o, s.width, s.height);
      svg += `<rect x="${s.x}" y="${s.y}" width="${s.width}" height="${s.height}" fill="none" stroke="gray"/>`;
      svg += `<text x="${s.x + s.width/2}" y="${s.y + s.height/2}" text-anchor="middle" dominant-baseline="middle" font-size="${headerFontSize}">${this.escapeXml(o)}</text>`;
      svg += `<line x1="${s.x}" y1="${s.height}" x2="${s.x}" y2="${svgHeight}" stroke="gray"/>`;
      svg += `<line x1="${s.x + s.width}" y1="${s.height}" x2="${s.x + s.width}" y2="${svgHeight}" stroke="gray"/>`;
    });
    const cellsByCol = new Map();
    shapesByCell.forEach(s => {
      if (!cellsByCol.has(s.col)) cellsByCol.set(s.col, []);
      cellsByCol.get(s.col).push(s);
    });
    cellsByCol.forEach(arr => arr.sort((a, b) => a.row - b.row));
    cellsByCol.forEach(arr => {
      let prevLinked = false;
      for (let i = 1; i < arr.length; i++) {
        const cur = arr[i];
        const prev = arr[i - 1];
        const curParent = this.parentMethodKeyOf(cur.key);
        if (curParent === null) { prevLinked = false; continue; }
        const isParent = prev.key === curParent;
        const isSibling = this.parentMethodKeyOf(prev.key) === curParent;
        if (isParent || (isSibling && prevLinked)) {
          const cx = cur.col * OBJECT_WIDTH + SHAPE_LEFT_MARGIN + SHAPE_WIDTH / 2;
          const prevBottom = yBase + prev.row * VERTICAL_GAP + SHAPE_HEIGHT;
          const curTop = yBase + cur.row * VERTICAL_GAP;
          svg += `<line x1="${cx}" y1="${prevBottom}" x2="${cx}" y2="${curTop}" stroke="#555555" stroke-dasharray="4 3"/>`;
          prevLinked = true;
        } else {
          prevLinked = false;
        }
      }
    });
    shapesByCell.forEach(s => {
      const x = s.col * OBJECT_WIDTH + SHAPE_LEFT_MARGIN;
      const y = yBase + s.row * VERTICAL_GAP;
      svg += this.drawShape(x, y, SHAPE_WIDTH, SHAPE_HEIGHT, s.text, s.type);
    });
    edges.forEach(e => {
      if (!e.to.dummy) {
        const fx = e.from.col * OBJECT_WIDTH + SHAPE_LEFT_MARGIN;
        const fy = yBase + e.from.row * VERTICAL_GAP;
        const tx = e.to.col * OBJECT_WIDTH + SHAPE_LEFT_MARGIN;
        const ty = yBase + e.to.row * VERTICAL_GAP;
        const fromShape = { x: fx, y: fy, width: SHAPE_WIDTH, height: SHAPE_HEIGHT };
        const toShape = { x: tx, y: ty, width: SHAPE_WIDTH, height: SHAPE_HEIGHT };
        if (e.bent) {
          svg += this.drawBentArrow(fromShape, toShape, e.bentIndex, VERTICAL_GAP);
        } else {
          svg += this.drawArrowBetweenShapes(fromShape, toShape);
        }
      }
    });
    svg += `</svg>`;
    return svg;
  },
  getShapeType(action) {
    const parts = action.split('.').map(p => p.replace(/\*+$/, ''));
    const lastPart = parts[parts.length - 1].toLowerCase();
    if (lastPart.startsWith("on")) return "event";
    if (lastPart === "message" || (parts.length > 1 && parts[parts.length - 2].toLowerCase() === "message")) return "message";
    if (action.includes('.')) return "data";
    return action ? "rectangle" : "";
  },
  getDisplayText(action) {
    const parts = action.split('.');
    let lastPart = parts[parts.length - 1];
    if (lastPart.toLowerCase().startsWith("on")) lastPart = lastPart.slice(2);
    return lastPart.replace(/\*+$/, '');
  },
  calculateFontSize(text, maxWidth, maxHeight, baseFontSize = 14) {
    const charWidthRatio = 0.6;
    const estimatedWidth = text.length * baseFontSize * charWidthRatio;
    const availableWidth = maxWidth * 0.9;
    if (estimatedWidth <= availableWidth) return baseFontSize;
    return Math.max(8, Math.floor(baseFontSize * (availableWidth / estimatedWidth)));
  },
  drawShape(x, y, width, height, text, shapeType) {
    let shape = '';
    let textMaxWidth = width;
    if (shapeType === 'event') {
      const radius = 10;
      shape = `<path d="M ${x+radius} ${y} H ${x+width-radius} Q ${x+width} ${y} ${x+width} ${y+radius} V ${y+height-radius} Q ${x+width} ${y+height} ${x+width-radius} ${y+height} H ${x+radius} Q ${x} ${y+height} ${x} ${y+height-radius} V ${y+radius} Q ${x} ${y} ${x+radius} ${y}" fill="#dfd" stroke="#333" stroke-width="2"/>`;
    } else if (shapeType === 'message') {
      const iconWidth = 18;
      const iconHeight = 12;
      const iconPad = 8;
      const iconX = x + iconPad;
      const iconY = y + (height - iconHeight) / 2;
      shape = `<rect x="${x}" y="${y}" width="${width}" height="${height}" fill="#f3e8ff" stroke="#333" stroke-width="2"/>`;
      shape += `<rect x="${iconX}" y="${iconY}" width="${iconWidth}" height="${iconHeight}" fill="#ffffff" stroke="#333" stroke-width="1.5"/>`;
      shape += `<path d="M ${iconX} ${iconY} L ${iconX + iconWidth/2} ${iconY + iconHeight*0.6} L ${iconX + iconWidth} ${iconY}" fill="none" stroke="#333" stroke-width="1.5"/>`;
      textMaxWidth = width - (iconPad + iconWidth + 6) * 2;
    } else if (shapeType === 'data') {
      shape = `<rect x="${x}" y="${y}" width="${width}" height="${height}" fill="#fff7d6" stroke="#333" stroke-width="2"/><line x1="${x+10}" y1="${y}" x2="${x+10}" y2="${y+height}" stroke="#333" stroke-width="2"/><line x1="${x+width-10}" y1="${y}" x2="${x+width-10}" y2="${y+height}" stroke="#333" stroke-width="2"/>`;
    } else {
      shape = `<rect x="${x}" y="${y}" width="${width}" height="${height}" fill="#bbf" stroke="#333" stroke-width="2"/>`;
    }
    const fontSize = this.calculateFontSize(text, textMaxWidth, height);
    shape += `<text x="${x+width/2}" y="${y+height/2}" text-anchor="middle" dominant-baseline="middle" font-size="${fontSize}">${this.escapeXml(text)}</text>`;
    return shape;
  },
  drawBentArrow(fromShape, toShape, bentIndex, verticalGap) {
    const startX = fromShape.x + fromShape.width / 2;
    const startY = fromShape.y + fromShape.height;
    const midY = toShape.y + toShape.height / 2;
    const arrowSize = 10;
    const targetIsRight = toShape.x > fromShape.x;
    let endX, path;
    if (targetIsRight) {
      endX = toShape.x;
      path = `M ${startX} ${startY} L ${startX} ${midY} L ${endX - arrowSize} ${midY}`;
    } else {
      endX = toShape.x + toShape.width;
      path = `M ${startX} ${startY} L ${startX} ${midY} L ${endX + arrowSize} ${midY}`;
    }
    return `<defs><marker id="arrowhead" markerWidth="10" markerHeight="7" refX="0" refY="3.5" orient="auto"><polygon points="0 0, 10 3.5, 0 7"/></marker></defs><path d="${path}" fill="none" stroke="#333" marker-end="url(#arrowhead)"/>`;
  },
  drawArrowBetweenShapes(fromShape, toShape) {
    const arrowSize = 10;
    if (fromShape.y === toShape.y) {
      const fromY = fromShape.y + fromShape.height / 2;
      let fromX, toX;
      if (fromShape.x < toShape.x) { fromX = fromShape.x + fromShape.width; toX = toShape.x; }
      else { fromX = fromShape.x; toX = toShape.x + toShape.width; }
      return this.drawArrow(fromX, fromY, toX, fromY);
    }
    if (fromShape.x === toShape.x) {
      const fromX = fromShape.x + fromShape.width / 2;
      let fromY, toY;
      if (fromShape.y < toShape.y) { fromY = fromShape.y + fromShape.height; toY = toShape.y; }
      else { fromY = fromShape.y; toY = toShape.y + toShape.height; }
      return this.drawArrow(fromX, fromY, fromX, toY);
    }
    if (fromShape.y < toShape.y) {
      const startX = fromShape.x + fromShape.width / 2;
      const startY = fromShape.y + fromShape.height;
      const midY = toShape.y + toShape.height / 2;
      let endX, path;
      if (fromShape.x < toShape.x) {
        endX = toShape.x;
        path = `M ${startX} ${startY} L ${startX} ${midY} L ${endX - arrowSize} ${midY}`;
      } else {
        endX = toShape.x + toShape.width;
        path = `M ${startX} ${startY} L ${startX} ${midY} L ${endX + arrowSize} ${midY}`;
      }
      return `<defs><marker id="arrowhead" markerWidth="10" markerHeight="7" refX="0" refY="3.5" orient="auto"><polygon points="0 0, 10 3.5, 0 7"/></marker></defs><path d="${path}" fill="none" stroke="#333" marker-end="url(#arrowhead)"/>`;
    } else {
      const midY = fromShape.y + fromShape.height / 2;
      let startX, endX, endY, path;
      if (fromShape.x < toShape.x) {
        startX = fromShape.x + fromShape.width;
        endX = toShape.x + toShape.width / 2;
        endY = toShape.y + toShape.height;
        path = `M ${startX} ${midY} L ${endX} ${midY} L ${endX} ${endY + arrowSize}`;
      } else {
        startX = fromShape.x;
        endX = toShape.x + toShape.width / 2;
        endY = toShape.y + toShape.height;
        path = `M ${startX} ${midY} L ${endX} ${midY} L ${endX} ${endY + arrowSize}`;
      }
      return `<defs><marker id="arrowhead" markerWidth="10" markerHeight="7" refX="0" refY="3.5" orient="auto"><polygon points="0 0, 10 3.5, 0 7"/></marker></defs><path d="${path}" fill="none" stroke="#333" marker-end="url(#arrowhead)"/>`;
    }
  },
  drawArrow(fromX, fromY, toX, toY) {
    const arrowSize = 10;
    const angle = Math.atan2(toY - fromY, toX - fromX);
    const x2 = toX - arrowSize * Math.cos(angle);
    const y2 = toY - arrowSize * Math.sin(angle);
    return `<defs><marker id="arrowhead" markerWidth="10" markerHeight="7" refX="0" refY="3.5" orient="auto"><polygon points="0 0, 10 3.5, 0 7"/></marker></defs><line x1="${fromX}" y1="${fromY}" x2="${x2}" y2="${y2}" stroke="#333" marker-end="url(#arrowhead)"/>`;
  }
};

export default JobFlowToSVG;
