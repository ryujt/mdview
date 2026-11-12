import test from 'node:test';
import assert from 'node:assert/strict';
import { stateToMermaid } from '../internal/mdview/static/js/state.js';

test('State node names cannot collide with inherited object properties', () => {
  const mermaid = stateToMermaid('__proto__ --> constructor\nconstructor --> toString\ntoString --> done');
  for (const [index, label] of ['__proto__', 'constructor', 'toString', 'done'].entries()) {
    assert.ok(mermaid.includes(`${String.fromCharCode(65 + index)}["${label}"]`));
  }
  assert.ok(mermaid.includes('A --> B'));
  assert.ok(mermaid.includes('B --> C'));
  assert.ok(mermaid.includes('C --> D'));
  assert.equal(mermaid.includes('[object Object]'), false);
});

test('State diagrams keep valid, unique identifiers beyond 26 nodes', () => {
  const script = Array.from({ length: 60 }, (_, index) => `state-${index} --> state-${index + 1}`).join('\n');
  const mermaid = stateToMermaid(script);
  const definitions = Array.from(mermaid.matchAll(/^    (\S+)\["state-(\d+)"\]$/gm));
  assert.equal(definitions.length, 61);
  assert.equal(new Set(definitions.map(match => match[1])).size, 61);
  for (const [, identifier] of definitions) assert.match(identifier, /^[A-Za-z][A-Za-z0-9]*$/);
  assert.ok(mermaid.includes('A["state-0"]'));
  assert.ok(mermaid.includes('Z["state-25"]'));
  assert.ok(mermaid.includes('Z --> S26'));
  assert.ok(mermaid.includes('S59 --> S60'));
  assert.equal((mermaid.match(/^    \S+ --> \S+$/gm) || []).length, 60);
});

test('State conversion preserves start, end, rounded states and event labels', () => {
  const mermaid = stateToMermaid('<s> --> (대기)\n(대기) --> <완료> : 요청 "확인"\n<완료> --> <e>');
  assert.ok(mermaid.includes('A(( ))'));
  assert.ok(mermaid.includes('B("대기")'));
  assert.ok(mermaid.includes('C{{"완료"}}'));
  assert.ok(mermaid.includes('D(( ))'));
  assert.ok(mermaid.includes('B -->|"요청 #quot;확인#quot;"| C'));
  assert.ok(mermaid.includes('style A fill:#fff,stroke:#000'));
  assert.ok(mermaid.includes('style D fill:#000,stroke:#000'));
});
