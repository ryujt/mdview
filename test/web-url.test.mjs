import test from 'node:test';
import assert from 'node:assert/strict';
import { normalizeWebURL } from '../internal/mdview/static/js/web-url.js';

test('scheme이 없으면 https를 붙인다', () => {
  assert.equal(normalizeWebURL('example.com/a'), 'https://example.com/a');
  assert.equal(normalizeWebURL('  localhost:3000 '), 'http://localhost:3000');
  assert.equal(normalizeWebURL('127.0.0.1:8642/x'), 'http://127.0.0.1:8642/x');
  assert.equal(normalizeWebURL('app.localhost'), 'http://app.localhost');
  assert.equal(normalizeWebURL('example.com:8080/a'), 'https://example.com:8080/a');
  assert.equal(normalizeWebURL('//ex.com'), 'https://ex.com');
});

test('http/https는 그대로 둔다', () => {
  assert.equal(normalizeWebURL('http://a.b/c?d=1'), 'http://a.b/c?d=1');
  assert.equal(normalizeWebURL('HTTPS://A.B'), 'HTTPS://A.B');
});

test('빈 입력과 다른 scheme은 빈 문자열', () => {
  assert.equal(normalizeWebURL(''), '');
  assert.equal(normalizeWebURL('   '), '');
  assert.equal(normalizeWebURL('file:///etc/passwd'), '');
  assert.equal(normalizeWebURL('javascript:alert(1)'), '');
});
