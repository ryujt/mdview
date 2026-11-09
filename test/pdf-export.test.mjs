import test from 'node:test';
import assert from 'node:assert/strict';
import {
  buildPrintableHtml,
  exportBaseName,
  splitHighlightedLines,
} from '../internal/mdview/static/js/pdf-export.js';

test('경로에서 확장자를 뺀 파일 이름을 PDF 기본 이름으로 쓴다', () => {
  assert.equal(exportBaseName('docs/guide.md'), 'guide');
  assert.equal(exportBaseName('C:\\work\\report.html'), 'report');
  assert.equal(exportBaseName('archive.tar.gz'), 'archive.tar');
  assert.equal(exportBaseName('.env'), '.env');
  assert.equal(exportBaseName(''), 'document');
  assert.equal(exportBaseName(null), 'document');
});

test('인쇄용 HTML에 <base>와 인쇄 스크립트를 넣는다', () => {
  const out = buildPrintableHtml('<html><head><title>t</title></head><body><p>x</p></body></html>', 'http://127.0.0.1:8642/a/b.html');
  assert.match(out, /<head><base href="http:\/\/127\.0\.0\.1:8642\/a\/b\.html">/);
  assert.ok(out.indexOf('<script>') > out.indexOf('<p>x</p>'));
  assert.ok(out.indexOf('<script>') < out.indexOf('</body>'));
});

test('문서에 이미 <base>가 있으면 그대로 두고, head·body가 없어도 동작한다', () => {
  const withBase = buildPrintableHtml('<head><base href="https://ex.com/"></head><body></body>', 'http://x/');
  assert.equal(withBase.match(/<base/g).length, 1);
  const bare = buildPrintableHtml('<p>x</p>', 'http://x/"y');
  assert.ok(bare.startsWith('<base href="http://x/&quot;y">'));
  assert.ok(bare.endsWith('</script>'));
});

test('head가 없으면 <base>를 <html> 또는 doctype 뒤에 넣어 표준 모드를 유지한다', () => {
  const noHead = buildPrintableHtml('<!DOCTYPE html>\n<html lang="ko"><title>x</title><body>hi</body></html>', 'http://h/a/b.html');
  assert.ok(noHead.startsWith('<!DOCTYPE html>\n<html lang="ko"><base href="http://h/a/b.html">'));
  const doctypeOnly = buildPrintableHtml('<!-- c --><!doctype html><p>x</p>', 'http://h/');
  assert.ok(doctypeOnly.startsWith('<!-- c --><!doctype html><base href="http://h/">'));
});

test('인쇄 스크립트는 마지막 </body> 앞에 넣는다', () => {
  const out = buildPrintableHtml('<html><head><script>var s="</body>";</script></head><body>x</body></html>', 'http://h/');
  assert.ok(out.includes('<script>var s="</body>";</script>'));
  assert.ok(out.endsWith('</script></body></html>'));
  assert.ok(out.lastIndexOf('<script>addEventListener') > out.indexOf('<body>x'));
});

test('기존 <base>의 상대 href는 원본 위치 기준 절대 URL로 바꾸고, href가 없으면 <base>를 넣는다', () => {
  const rel = buildPrintableHtml('<head><base href="../css/" target="_blank"></head><body></body>', 'http://h/a/b/c.html');
  assert.match(rel, /<base href="http:\/\/h\/a\/css\/" target="_blank">/);
  assert.equal(rel.match(/<base/g).length, 1);
  const noHref = buildPrintableHtml('<head><base target="_blank"></head><body></body>', 'http://h/a.html');
  assert.match(noHref, /<head><base href="http:\/\/h\/a\.html"><base target="_blank">/);
});

test('인쇄 직전에 문서 제목을 파일 이름으로 바꾸고 시작 신호를 보낸다', () => {
  const out = buildPrintableHtml('<body></body>', 'http://h/', 'a</script>b');
  assert.ok(out.includes('document.title="a\\u003c/script>b";print()'));
  assert.ok(out.includes("postMessage(\"mdview-print-start\""));
  assert.equal(out.match(/<\/script>/g).length, 1);
});

test('여러 줄에 걸친 span은 줄 끝에서 닫고 다음 줄에서 다시 연다', () => {
  assert.deepEqual(splitHighlightedLines('<span class="c">/* a\nb */</span>\nx'), [
    '<span class="c">/* a</span>',
    '<span class="c">b */</span>',
    'x',
  ]);
  assert.deepEqual(splitHighlightedLines(''), ['']);
});
