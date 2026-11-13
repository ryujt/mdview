import test from 'node:test';
import assert from 'node:assert/strict';
import {
  buildPrintableHtml,
  exportBaseName,
  splitHighlightedLines,
} from '../internal/mdview/static/js/pdf-export.js';

test('uses the file name from the path, minus its extension, as the default PDF name', () => {
  assert.equal(exportBaseName('docs/guide.md'), 'guide');
  assert.equal(exportBaseName('C:\\work\\report.html'), 'report');
  assert.equal(exportBaseName('archive.tar.gz'), 'archive.tar');
  assert.equal(exportBaseName('.env'), '.env');
  assert.equal(exportBaseName(''), 'document');
  assert.equal(exportBaseName(null), 'document');
});

test('inserts <base> and the print script into printable HTML', () => {
  const out = buildPrintableHtml('<html><head><title>t</title></head><body><p>x</p></body></html>', 'http://127.0.0.1:8642/a/b.html');
  assert.match(out, /<head><base href="http:\/\/127\.0\.0\.1:8642\/a\/b\.html">/);
  assert.ok(out.indexOf('<script>') > out.indexOf('<p>x</p>'));
  assert.ok(out.indexOf('<script>') < out.indexOf('</body>'));
});

test('keeps an existing <base> and works without head or body', () => {
  const withBase = buildPrintableHtml('<head><base href="https://ex.com/"></head><body></body>', 'http://x/');
  assert.equal(withBase.match(/<base/g).length, 1);
  const bare = buildPrintableHtml('<p>x</p>', 'http://x/"y');
  assert.ok(bare.startsWith('<base href="http://x/&quot;y">'));
  assert.ok(bare.endsWith('</script>'));
});

test('without head, inserts <base> after <html> or the doctype to keep standards mode', () => {
  const noHead = buildPrintableHtml('<!DOCTYPE html>\n<html lang="ko"><title>x</title><body>hi</body></html>', 'http://h/a/b.html');
  assert.ok(noHead.startsWith('<!DOCTYPE html>\n<html lang="ko"><base href="http://h/a/b.html">'));
  const doctypeOnly = buildPrintableHtml('<!-- c --><!doctype html><p>x</p>', 'http://h/');
  assert.ok(doctypeOnly.startsWith('<!-- c --><!doctype html><base href="http://h/">'));
});

test('inserts the print script before the last </body>', () => {
  const out = buildPrintableHtml('<html><head><script>var s="</body>";</script></head><body>x</body></html>', 'http://h/');
  assert.ok(out.includes('<script>var s="</body>";</script>'));
  assert.ok(out.endsWith('</script></body></html>'));
  assert.ok(out.lastIndexOf('<script>addEventListener') > out.indexOf('<body>x'));
});

test('rewrites a relative href on an existing <base> to an absolute URL from the original location, and inserts <base> when it has no href', () => {
  const rel = buildPrintableHtml('<head><base href="../css/" target="_blank"></head><body></body>', 'http://h/a/b/c.html');
  assert.match(rel, /<base href="http:\/\/h\/a\/css\/" target="_blank">/);
  assert.equal(rel.match(/<base/g).length, 1);
  const noHref = buildPrintableHtml('<head><base target="_blank"></head><body></body>', 'http://h/a.html');
  assert.match(noHref, /<head><base href="http:\/\/h\/a\.html"><base target="_blank">/);
});

test('sets the document title to the file name and sends the start signal just before printing', () => {
  const out = buildPrintableHtml('<body></body>', 'http://h/', 'a</script>b');
  assert.ok(out.includes('document.title="a\\u003c/script>b";print()'));
  assert.ok(out.includes("postMessage(\"mdview-print-start\""));
  assert.equal(out.match(/<\/script>/g).length, 1);
});

test('closes spans that cross lines at the line end and reopens them on the next line', () => {
  assert.deepEqual(splitHighlightedLines('<span class="c">/* a\nb */</span>\nx'), [
    '<span class="c">/* a</span>',
    '<span class="c">b */</span>',
    'x',
  ]);
  assert.deepEqual(splitHighlightedLines(''), ['']);
});
