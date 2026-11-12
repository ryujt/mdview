# ZIP export dependencies

The checked-in ES modules support ZIP export without an extra CDN request or a
Node.js installation at build/runtime. Markdown preview still uses the existing
CDN resources. `markdown-export.js` uses its own Marked instance so export parsing
does not change the preview's tokenizer.

- Marked 12.0.2 — https://github.com/markedjs/marked (MIT)
- JSZip 3.10.1 — https://github.com/Stuk/jszip (MIT option)
- `LICENSES.txt` includes the resolved JSZip dependency notices and is included
  in the app's `THIRD-PARTY-NOTICES.txt`.

To regenerate, install the pinned development tools in a temporary directory and
pass that directory to the script from the mdview repository root:

```sh
vendor_tools=$(mktemp -d)
npm install --prefix "$vendor_tools" --no-audit --no-fund esbuild@0.25.12 marked@12.0.2 jszip@3.10.1
node packaging/vendor-export.mjs "$vendor_tools"
node --test test/*.test.mjs
```

The export source follows tools.camp's `src/lib/markdown-export.js`, with local
imports for these vendor modules. Keep the conversion regression tests in sync
when changing the export behavior.
