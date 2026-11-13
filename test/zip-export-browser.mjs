// Opt-in real CLI/Chrome gate. Uses isolated HOME/config and never records audio.
// MDVIEW_ZIP_BROWSER=1 PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs \
// CHROME_EXECUTABLE=/path/to/chrome node cli/mdview/test/zip-export-browser.mjs
import fs from 'node:fs/promises';
import os from 'node:os';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import JSZip from '../internal/mdview/static/vendor/jszip.js';

const cliRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');

function command(executable, args, options = {}) {
  return new Promise((resolveCommand, reject) => {
    const child = spawn(executable, args, { stdio: ['ignore', 'pipe', 'pipe'], ...options });
    let output = '';
    child.stdout.on('data', chunk => { output += chunk; });
    child.stderr.on('data', chunk => { output += chunk; });
    child.on('error', reject);
    child.on('exit', code => code === 0 ? resolveCommand(output) : reject(new Error(`${executable} failed (${code}): ${output}`)));
  });
}

async function startServer(binary, mode, fixtures, directory) {
  const home = join(directory, 'home-' + mode);
  const bin = join(directory, 'bin');
  await fs.mkdir(home, { recursive: true });
  await fs.mkdir(bin, { recursive: true });
  // The CLI normally opens the system browser; only this child's PATH is changed.
  for (const opener of ['open', 'xdg-open']) {
    await fs.writeFile(join(bin, opener), '#!/bin/sh\nexit 0\n', { mode: 0o700 });
  }
  const child = spawn(binary, ['-port', '0', ...(mode === 'file' ? [join(fixtures, 'document.md')] : [])], {
    cwd: fixtures,
    env: { ...process.env, HOME: home, USERPROFILE: home, XDG_CONFIG_HOME: join(home, '.config'),
      OPENAI_API_KEY: '', PATH: bin + ':' + process.env.PATH, DEBUG_MODE: 'false', LOG_TO_FILE: 'false' },
    stdio: ['ignore', 'pipe', 'pipe'],
  });
  let output = '';
  const base = await new Promise((resolveBase, reject) => {
    const timeout = setTimeout(() => reject(new Error('CLI startup timed out: ' + output)), 15000);
    const onData = chunk => {
      output += chunk;
      const match = output.match(/Serving .* at (http:\/\/[^\s]+)/);
      if (match) { clearTimeout(timeout); resolveBase(match[1]); }
    };
    child.stdout.on('data', onData);
    child.stderr.on('data', onData);
    child.once('error', error => { clearTimeout(timeout); reject(error); });
    child.once('exit', code => { clearTimeout(timeout); reject(new Error(`CLI exited (${code}): ${output}`)); });
  }).catch(error => { child.kill('SIGTERM'); throw error; });
  return { base, async stop() {
    if (child.exitCode !== null) return;
    child.kill('SIGTERM');
    await Promise.race([once(child, 'exit'), new Promise(resolveStop => setTimeout(resolveStop, 5000))]);
    if (child.exitCode === null) child.kill('SIGKILL');
  } };
}

async function run() {
  const moduleName = process.env.PLAYWRIGHT_MODULE || 'playwright';
  const { chromium } = await import(moduleName.startsWith('/') ? pathToFileURL(moduleName).href : moduleName);
  const directory = await fs.mkdtemp(join(os.tmpdir(), 'mdview-zip-browser-'));
  const binary = process.env.MDVIEW_BINARY ? resolve(process.env.MDVIEW_BINARY) : join(directory, 'mdview');
  if (!process.env.MDVIEW_BINARY) await command('go', ['build', '-o', binary, './cmd'], { cwd: cliRoot });
  const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });
  const report = { passed: false, checks: [], failures: [], errors: [], blockedRequests: [], images: [], modes: [] };
  const check = (condition, label, details) => {
    report.checks.push({ label, passed: Boolean(condition) });
    if (!condition) { report.failures.push({ label, details }); console.error('FAIL ' + label + ' ' + JSON.stringify(details || {})); }
  };
  const fence = (language, body) => '```' + language + '\n' + body + '\n```';
  const source = [
    '# CLI ZIP source\n\nThe description  stays **exactly** as is.',
    fence('navigation', 'Home --> (/save) : save\n(/save) --> <Done> : success'),
    fence('state', '<s> --> (Waiting)\n(Waiting) --> <e> : finish'),
    fence('mermaid', 'graph LR\nOriginal --> Preserved'),
    '---',
    fence('jobflow', 'orchestrator: Order\nObject: Order, Payment\nOrder.Start --> Payment.Approve\nPayment.Approve.result --> Order.Complete'),
    fence('jobflow', 'Object: User, Server\nUser.Query --> Server.Search').split('\n').map(line => '> ' + line).join('\n'),
  ].join('\n\n') + '\n';
  let activePage = null;
  try {
    for (const mode of ['file', 'folder']) {
      const fixtures = join(directory, 'fixtures-' + mode);
      await fs.mkdir(fixtures);
      await fs.writeFile(join(fixtures, 'document.md'), source);
      await fs.writeFile(join(fixtures, 'code.js'), 'const source = "code document";\n');
      const server = await startServer(binary, mode, fixtures, directory);
      const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, acceptDownloads: true });
      const page = await context.newPage();
      activePage = page;
      let downloadEvents = 0;
      page.on('download', () => { downloadEvents++; });
      page.on('pageerror', error => report.errors.push({ mode, message: error.message }));
      await context.route('**/*', route => {
        const url = new URL(route.request().url());
        if (url.origin === new URL(server.base).origin && !url.pathname.startsWith('/api/recording') && !url.pathname.startsWith('/api/settings')) return route.continue();
        if (['cdn.jsdelivr.net', 'cdnjs.cloudflare.com'].includes(url.hostname)) return route.continue();
        report.blockedRequests.push({ mode, url: url.href });
        return route.abort();
      });
      await context.addInitScript(() => {
        window.__zipPrintCalls = 0;
        window.print = () => { window.__zipPrintCalls++; };
        Object.defineProperty(window, 'showSaveFilePicker', { configurable: true, value: async () => { throw new DOMException('Headless download fallback', 'NotAllowedError'); } });
      });
      try {
        await page.goto(server.base);
        await page.locator('#export-menu-button').waitFor();
        if (mode === 'folder') {
          await page.locator('#tree .tree-file[data-path="document.md"]').waitFor();
          await page.locator('#export-menu-button').click();
          check(await page.locator('#export-zip').getAttribute('aria-disabled') === 'true', mode + ': ZIP is disabled with no selected document');
          await page.keyboard.press('Escape');
          await page.locator('#tree .tree-file[data-path="document.md"]').click();
        }
        await page.locator('#preview').getByRole('heading', { name: 'CLI ZIP source', exact: true }).waitFor();
        await page.waitForFunction(() => document.querySelectorAll('#preview .mermaid[data-processed="true"] svg').length === 3, null, { timeout: 30000 });
        await page.locator('#export-menu-button').focus();
        await page.keyboard.press('ArrowDown');
        check(await page.locator('#export-menu-button').getAttribute('aria-expanded') === 'true', mode + ': keyboard opens the export menu');
        check(await page.locator('#export-pdf').getAttribute('aria-disabled') !== 'true', mode + ': existing PDF export remains available');
        await page.keyboard.press('Escape');
        check(await page.locator('#export-menu-button').evaluate(element => document.activeElement === element), mode + ': Escape returns focus to the menu button');
        const shortcut = await page.evaluate(() => /mac|iphone|ipad/i.test((navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform)) ? 'Meta+P' : 'Control+P';
        await page.keyboard.press(shortcut);
        check(await page.evaluate(() => window.__zipPrintCalls) === 1, mode + ': print shortcut still invokes PDF printing');

        let downloadIndex = 0;
        async function downloadZip() {
          await page.locator('#export-menu-button').click();
          check(await page.locator('#export-zip').getAttribute('aria-disabled') !== 'true', mode + ': ZIP is enabled for Markdown');
          const pending = page.waitForEvent('download', { timeout: 30000 });
          await page.locator('#export-zip').click();
          const download = await pending;
          check(download.suggestedFilename() === 'document.zip', mode + ': download uses the current document basename');
          const bytes = await fs.readFile(await download.path());
          await fs.writeFile(join(directory, `${mode}-${++downloadIndex}.zip`), bytes);
          return JSZip.loadAsync(bytes);
        }
        const zip = await downloadZip();
        const markdown = await zip.file('document.md')?.async('string');
        check(Boolean(markdown), mode + ': ZIP contains document.md');
        if (!markdown) throw new Error('ZIP is missing document.md');
        check(!/^\s*(?:> )?```(?:navigation|state|jobflow)\s*$/m.test(markdown), mode + ': custom diagram fences become standard Markdown content');
        check((markdown.match(/^```mermaid$/gm) || []).length === 3, mode + ': navigation/state convert to Mermaid and original Mermaid remains');
        check(markdown.startsWith('# CLI ZIP source\n\nThe description  stays **exactly** as is.'), mode + ': ordinary Markdown remains byte-exact');
        const references = [...markdown.matchAll(/!\[Jobflow \d+\]\((images\/[^)]+)\)/g)].map(match => match[1]);
        const imageFiles = Object.keys(zip.files).filter(name => !zip.files[name].dir && name.startsWith('images/'));
        check(references.length === 2 && new Set(references).size === 2 && JSON.stringify([...references].sort()) === JSON.stringify(imageFiles.sort()), mode + ': both full-document PNGs are linked to matching packaged files', { references, imageFiles });
        for (const filename of references) {
          const bytes = Buffer.from(await zip.file(filename).async('uint8array'));
          check(bytes.subarray(0, 8).equals(Buffer.from([137, 80, 78, 71, 13, 10, 26, 10])), mode + ': ' + filename + ' is PNG');
          await fs.writeFile(join(directory, mode + '-' + filename.split('/').at(-1)), bytes);
          const pixels = await page.evaluate(async data => {
            const image = new Image(); image.src = 'data:image/png;base64,' + data; await image.decode();
            const canvas = document.createElement('canvas'); canvas.width = image.width; canvas.height = image.height;
            const context = canvas.getContext('2d'); context.drawImage(image, 0, 0);
            const rgba = context.getImageData(0, 0, canvas.width, canvas.height).data;
            let ink = 0;
            for (let i = 0; i < rgba.length; i += 4) if (rgba[i + 3] && (rgba[i] < 235 || rgba[i + 1] < 235 || rgba[i + 2] < 235)) ink++;
            return { width: canvas.width, height: canvas.height, ink };
          }, bytes.toString('base64'));
          report.images.push({ mode, filename, ...pixels });
          check(pixels.width > 1 && pixels.height > 1 && pixels.ink > 100, mode + ': ' + filename + ' contains visible diagram pixels', pixels);
        }
        const imageData = Object.fromEntries(await Promise.all(references.map(async name => [name, 'data:image/png;base64,' + await zip.file(name).async('base64')])));
        const plainViewer = await page.evaluate(async ({ markdown, imageData }) => {
          // Plain Markdown + Mermaid, without mdview's custom diagram renderer.
          const article = document.createElement('article');
          article.style.cssText = 'position:absolute;left:-10000px;width:1000px;';
          article.innerHTML = window.marked.parse(markdown);
          document.body.appendChild(article);
          try {
            const mermaids = [...article.querySelectorAll('pre code.language-mermaid')].map(code => {
              const element = document.createElement('div');
              element.className = 'mermaid'; element.textContent = code.textContent;
              code.parentElement.replaceWith(element);
              return element;
            });
            await window.mermaid.run({ nodes: mermaids });
            await Promise.all([...article.querySelectorAll('img')].map(async image => {
              image.src = imageData[image.getAttribute('src')] || '';
              await image.decode();
            }));
            return { mermaids: article.querySelectorAll('.mermaid svg').length,
              images: [...article.querySelectorAll('img')].filter(image => image.naturalWidth > 0).length,
              customCode: article.querySelectorAll('code.language-navigation,code.language-state,code.language-jobflow').length };
          } finally { article.remove(); }
        }, { markdown, imageData });
        check(plainViewer.mermaids === 3 && plainViewer.images === 2 && plainViewer.customCode === 0, mode + ': package renders in a plain Markdown + Mermaid viewer', plainViewer);
        check(await fs.readFile(join(fixtures, 'document.md'), 'utf8') === source, mode + ': exporting does not change the source file');
        const changed = source.replace('CLI ZIP source', 'CLI ZIP changed') + '\nIncludes latest change\n';
        await fs.writeFile(join(fixtures, 'document.md'), changed);
        await page.locator('#preview').getByRole('heading', { name: 'CLI ZIP changed', exact: true }).waitFor();
        const updatedZip = await downloadZip();
        const updated = await updatedZip.file('document.md').async('string');
        check(updated.includes('CLI ZIP changed') && updated.endsWith('\nIncludes latest change\n'), mode + ': ZIP includes the latest WebSocket file change');
        const invalid = '# CLI ZIP invalid jobflow\n\n' + fence('jobflow', 'no objects or actions') + '\n';
        await fs.writeFile(join(fixtures, 'document.md'), invalid);
        await page.locator('#preview').getByRole('heading', { name: 'CLI ZIP invalid jobflow', exact: true }).waitFor();
        await page.locator('#export-menu-button').click();
        const beforeFailure = downloadEvents;
        await page.locator('#export-zip').click();
        await page.getByText('ZIP creation failed:', { exact: false }).waitFor();
        await page.locator('#export-menu-button').click();
        check(await page.locator('#export-zip').getAttribute('aria-disabled') !== 'true', mode + ': failed PNG export releases the menu control');
        check(downloadEvents === beforeFailure, mode + ': failed PNG export does not download a broken ZIP');
        await page.keyboard.press('Escape');
        await fs.writeFile(join(fixtures, 'document.md'), changed);
        await page.locator('#preview').getByRole('heading', { name: 'CLI ZIP changed', exact: true }).waitFor();
        const recovered = await downloadZip();
        check((await recovered.file('document.md').async('string')).includes('CLI ZIP changed'), mode + ': a valid export succeeds after PNG failure');
        if (mode === 'folder') {
          await page.locator('#tree .tree-file[data-path="code.js"]').click();
          await page.locator('#preview .code-viewer').waitFor();
          await page.locator('#export-menu-button').click();
          check(await page.locator('#export-zip').getAttribute('aria-disabled') === 'true', mode + ': ZIP is disabled for source-code files');
          check(await page.locator('#export-pdf').getAttribute('aria-disabled') !== 'true', mode + ': source-code PDF export remains available');
          await page.keyboard.press('Escape');
        }
        await page.screenshot({ path: join(directory, mode + '.png'), animations: 'disabled' });
        report.modes.push(mode);
      } finally {
        await context.close();
        await server.stop();
      }
    }
    check(report.errors.length === 0, 'both modes have no browser runtime errors', report.errors);
    check(report.blockedRequests.length === 0, 'ZIP workflow makes no recording/settings/other external requests', report.blockedRequests);
    report.passed = report.failures.length === 0;
  } catch (error) {
    report.error = error.stack;
    console.error(error.stack);
    await activePage?.screenshot({ path: join(directory, 'failure.png') }).catch(() => {});
  } finally {
    await browser.close();
    await fs.writeFile(join(directory, 'result.json'), JSON.stringify(report, null, 2));
    if (!report.passed) process.exitCode = 1;
    console.log(JSON.stringify({ passed: report.passed, checks: report.checks.length, failures: report.failures.length, artifacts: directory }));
  }
}

if (process.env.MDVIEW_ZIP_BROWSER === '1') await run();
else console.log('SKIP mdview ZIP: set MDVIEW_ZIP_BROWSER=1 with Go, Playwright and Chrome.');
