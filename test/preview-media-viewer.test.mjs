import test from 'node:test';
import assert from 'node:assert/strict';
import { fencedSource } from '../internal/mdview/static/js/preview-media-viewer.js';

test('다이어그램 코드를 시작·끝 펜스와 함께 만든다', () => {
  assert.equal(fencedSource('graph TD\n  A --> B', 'mermaid'), '```mermaid\ngraph TD\n  A --> B\n```');
  assert.equal(fencedSource('Main.run -> Worker.do\n\n', 'jobflow'), '```jobflow\nMain.run -> Worker.do\n```');
});

test('코드 안 백틱보다 긴 펜스를 쓴다', () => {
  assert.equal(fencedSource('a\n```x\nb', 'layout'), '````layout\na\n```x\nb\n````');
});
