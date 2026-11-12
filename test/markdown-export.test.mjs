import test from 'node:test';
import assert from 'node:assert/strict';
import JSZip from '../internal/mdview/static/vendor/jszip.js';
import { prepareMarkdownExport, createMarkdownZip } from '../internal/mdview/static/js/markdown-export.js';
import { navigationToMermaid } from '../internal/mdview/static/js/navigation.js';
import { stateToMermaid } from '../internal/mdview/static/js/state.js';

const png = 'data:image/png;base64,iVBORw0KGgo=';
const jobflow = 'Object: Client, Server\nClient.Start --> Server.Handle';
const fence = (language, body) => `\`\`\`${language}\n${body}\n\`\`\``;

test('a mixed document exports portable Mermaid and referenced jobflow PNGs in document order', async () => {
  const navigation = '시작 --> (/저장) : 클릭\n(/저장) --> 완료 : 성공';
  const state = '<s> --> (대기)\n(대기) --> <e> : 종료';
  const source = ['# 원본 문서', fence('navigation', navigation), '중간 설명', fence('jobflow', jobflow), fence('state', state), '끝.'].join('\n\n');
  const calls = [];
  const result = await prepareMarkdownExport(source, { renderJobflow: async script => { calls.push(script); return png; } });
  assert.deepEqual(calls, [jobflow]);
  assert.deepEqual(result.images, [{ filename: 'jobflow-1.png', dataUrl: png }]);
  assert.equal(result.markdown, ['# 원본 문서', fence('mermaid', navigationToMermaid(navigation).trimEnd()), '중간 설명', '![잡플로우 1](images/jobflow-1.png)', fence('mermaid', stateToMermaid(state).trimEnd()), '끝.'].join('\n\n'));
  assert.match(result.markdown, /시작/);
  assert.match(result.markdown, /클릭/);
  assert.match(result.markdown, /종료/);
  assert.doesNotMatch(result.markdown, /^```(?:navigation|state|jobflow)$/m);
});

test('every page and repeated jobflow receives a distinct ordered image reference', async () => {
  const bodies = [jobflow, 'Object: Third\nThird.Later', jobflow];
  const source = bodies.map((body, index) => `# 페이지 ${index + 1}\n\n${fence('jobflow', body)}`).join('\n\n---\n\n');
  const calls = [];
  const result = await prepareMarkdownExport(source, { renderJobflow: async script => {
    calls.push(script);
    return `data:image/png;base64,${Buffer.from(`image ${calls.length}`).toString('base64')}`;
  } });
  assert.deepEqual(calls, bodies);
  assert.deepEqual(result.images.map(image => image.filename), ['jobflow-1.png', 'jobflow-2.png', 'jobflow-3.png']);
  assert.equal(new Set(result.images.map(image => image.dataUrl)).size, 3);
  assert.equal((result.markdown.match(/^---$/gm) || []).length, 2);
  assert.deepEqual([...result.markdown.matchAll(/!\[잡플로우 (\d)\]\(images\/([^)]*)\)/g)].map(match => [match[1], match[2]]), [['1', 'jobflow-1.png'], ['2', 'jobflow-2.png'], ['3', 'jobflow-3.png']]);
});

test('ordinary Markdown, existing Mermaid, layout, and code retain their exact source', async () => {
  const source = '---\r\ntitle: 원문\r\n---\r\n\r\n# 제목  \r\n\r\n[링크](https://example.com)와 ![기존 이미지](pictures/a.png)\r\n\r\n'
    + fence('mermaid', 'graph LR\nA --> B') + '\n\n'
    + fence('layout', 'Screen V Header, Main') + '\n\n'
    + fence('javascript', 'const navigation = "A --> B";') + '\n\n끝\t';
  const result = await prepareMarkdownExport(source, { renderJobflow: () => assert.fail('no jobflow should be rendered') });
  assert.equal(result.markdown, source);
  assert.deepEqual(result.images, []);
  assert.deepEqual(await prepareMarkdownExport(''), { markdown: '', images: [] });
});

test('nested diagram examples inside text and wider ordinary fences stay literal', async () => {
  const examples = [
    fence('text', [fence('navigation', 'Example --> Literal'), fence('jobflow', jobflow)].join('\n')),
    '````markdown\n' + fence('state', '<s> --> <e>') + '\n' + fence('jobflow', jobflow) + '\n````',
    '~~~~markdown\n' + fence('navigation', 'Still --> Literal') + '\n~~~~',
  ].join('\n\n');
  const source = examples + '\n\n' + fence('navigation', 'Actual --> Diagram');
  const result = await prepareMarkdownExport(source, { renderJobflow: () => assert.fail('literal examples must not create PNGs') });
  assert.ok(result.markdown.startsWith(examples + '\n\n'));
  assert.ok(result.markdown.endsWith(fence('mermaid', navigationToMermaid('Actual --> Diagram').trimEnd())));
  assert.deepEqual(result.images, []);
});

test('tilde fences, mixed case, whitespace, indentation and CRLF preserve document boundaries', async () => {
  const navigation = 'Home --> Dashboard : 열기';
  const source = `앞\r\n\r\n  ~~~  NaViGaTiOn \t\r\n  ${navigation}\r\n  ~~~~\t\r\n\r\n~~~ JOBFLOW \t\r\n${jobflow.replaceAll('\n', '\r\n')}\r\n~~~`;
  const calls = [];
  const result = await prepareMarkdownExport(source, { renderJobflow: async script => { calls.push(script); return png; } });
  const expectedMermaid = navigationToMermaid(navigation).trimEnd().split('\n').map(line => '  ' + line).join('\r\n');
  assert.equal(result.markdown, `앞\r\n\r\n  ~~~mermaid\r\n${expectedMermaid}\r\n  ~~~~\t\r\n\r\n![잡플로우 1](images/jobflow-1.png)`);
  assert.deepEqual(calls, [jobflow]);
  assert.equal(result.markdown.endsWith('\n'), false);
});

test('unclosed fences and mismatched or shorter closers do not expose nested examples', async () => {
  const examples = [
    '````markdown\n' + fence('jobflow', jobflow),
    '~~~text\n' + fence('navigation', 'Example --> Literal'),
    '```javascript\n~~~\n' + fence('jobflow', jobflow) + '\n```',
  ];
  for (const source of examples) {
    const result = await prepareMarkdownExport(source, { renderJobflow: () => assert.fail('unclosed enclosing examples must remain literal') });
    assert.equal(result.markdown, source);
    assert.deepEqual(result.images, []);
  }
});

test('PNG failures reject the export instead of returning dangling image references', async () => {
  const source = fence('jobflow', jobflow);
  await assert.rejects(prepareMarkdownExport(source, { renderJobflow: async () => { throw new Error('injected rasterization failure'); } }), /injected rasterization failure/);
  for (const invalid of [null, '', 'data:image/svg+xml;base64,AAA']) {
    await assert.rejects(prepareMarkdownExport(source, { renderJobflow: async () => invalid }), /이미지를 생성할 수 없습니다/);
  }
});

test('the real ZIP contains converted document.md without redundant diagram image files', async () => {
  const source = '# ZIP\n\n' + fence('navigation', 'Home --> Dashboard') + '\n\n' + fence('state', '<s> --> (Ready)\n(Ready) --> <e>');
  const { blob, imageCount } = await createMarkdownZip(source);
  const zip = await JSZip.loadAsync(await blob.arrayBuffer());
  assert.equal(imageCount, 0);
  assert.deepEqual(Object.keys(zip.files), ['document.md']);
  const markdown = await zip.file('document.md').async('string');
  assert.equal((markdown.match(/^```mermaid$/gm) || []).length, 2);
  assert.doesNotMatch(markdown, /^```(?:navigation|state)$/m);
  assert.match(markdown, /Dashboard/);
  assert.match(markdown, /Ready/);
});

test('blockquote and list diagrams retain containers and source locations after references', async () => {
  const navigation = 'Home --> Dashboard : 열기';
  const state = '<s> --> (Ready)\n(Ready) --> <e>';
  const before = '[reference]: https://example.com\n\n앞 문단\n\n';
  const quote = fence('navigation', navigation).split('\n').map(line => '> ' + line).join('\n');
  const list = fence('state', state).split('\n').map(line => '    ' + line).join('\n');
  const nestedJobflow = fence('jobflow', jobflow).split('\n').map(line => '>    ' + line).join('\n');
  const source = before + quote + '\n\n1. 목록\n\n' + list + '\n\n> 1. 중첩 목록\n>\n' + nestedJobflow + '\n\n끝';
  const calls = [];
  const result = await prepareMarkdownExport(source, { renderJobflow: async script => { calls.push(script); return png; } });
  const expectedQuote = fence('mermaid', navigationToMermaid(navigation).trimEnd()).split('\n').map(line => '> ' + line).join('\n');
  const expectedList = fence('mermaid', stateToMermaid(state).trimEnd()).split('\n').map(line => '    ' + line).join('\n');
  assert.equal(result.markdown, before + expectedQuote + '\n\n1. 목록\n\n' + expectedList + '\n\n> 1. 중첩 목록\n>\n>    ![잡플로우 1](images/jobflow-1.png)\n\n끝');
  assert.deepEqual(calls, [jobflow]);
  assert.deepEqual(result.images, [{ filename: 'jobflow-1.png', dataUrl: png }]);
});

test('fences starting directly on list items keep markers only on the opening line', async () => {
  const navigation = 'Start --> Done';
  const source = '- ```navigation\n  ' + navigation + '\n  ```\n\n- ```jobflow\n  ' + jobflow.replaceAll('\n', '\n  ') + '\n  ```';
  const calls = [];
  const result = await prepareMarkdownExport(source, { renderJobflow: async script => { calls.push(script); return png; } });
  const mermaid = navigationToMermaid(navigation).trimEnd().split('\n').map(line => '  ' + line).join('\n');
  assert.equal(result.markdown, '- ```mermaid\n' + mermaid + '\n  ```\n\n- ![잡플로우 1](images/jobflow-1.png)');
  assert.deepEqual(calls, [jobflow]);
});

test('quoted text/wider fences, indented code and metadata examples remain literal', async () => {
  const quotedText = fence('text', fence('jobflow', jobflow)).split('\n').map(line => '> ' + line).join('\n');
  const quotedWide = ('````markdown\n' + fence('navigation', 'Example --> Literal') + '\n````').split('\n').map(line => '> ' + line).join('\n');
  const indented = fence('jobflow', jobflow).split('\n').map(line => '    ' + line).join('\n');
  const frontmatter = '---\nexample: |\n' + fence('state', 'Example --> Literal').split('\n').map(line => '  ' + line).join('\n') + '\n---\n\n';
  const source = frontmatter + [quotedText, quotedWide, indented, '<!--\n' + fence('jobflow', jobflow) + '\n-->'].join('\n\n');
  const result = await prepareMarkdownExport(source, { renderJobflow: () => assert.fail('literal examples must not create PNGs') });
  assert.equal(result.markdown, source);
  assert.deepEqual(result.images, []);
});

test('image numbering follows document order across nested and top-level diagrams', async () => {
  const quote = fence('jobflow', jobflow).split('\n').map(line => '> ' + line).join('\n');
  const source = quote + '\n\n' + fence('jobflow', jobflow) + '\n\n- ```jobflow\n  ' + jobflow.replaceAll('\n', '\n  ') + '\n  ```';
  const result = await prepareMarkdownExport(source, { renderJobflow: async () => png });
  assert.deepEqual(result.images.map(image => image.filename), ['jobflow-1.png', 'jobflow-2.png', 'jobflow-3.png']);
  assert.equal(result.markdown, '> ![잡플로우 1](images/jobflow-1.png)\n\n![잡플로우 2](images/jobflow-2.png)\n\n- ![잡플로우 3](images/jobflow-3.png)');
});
