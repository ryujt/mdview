import test from 'node:test';
import assert from 'node:assert/strict';
import { fencedSource } from '../internal/mdview/static/js/preview-media-viewer.js';

test('builds diagram code with opening and closing fences', () => {
  assert.equal(fencedSource('graph TD\n  A --> B', 'mermaid'), '```mermaid\ngraph TD\n  A --> B\n```');
  assert.equal(fencedSource('Main.run -> Worker.do\n\n', 'jobflow'), '```jobflow\nMain.run -> Worker.do\n```');
});

test('uses a fence longer than any backtick run in the code', () => {
  assert.equal(fencedSource('a\n```x\nb', 'layout'), '````layout\na\n```x\nb\n````');
});
