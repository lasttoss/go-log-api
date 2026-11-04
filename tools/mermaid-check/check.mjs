#!/usr/bin/env node
// Parses every Mermaid diagram in this repository with the real Mermaid parser.
//
// Why this exists: a broken diagram does not fail a build. GitHub only shows an "Unable to render
// rich display" box to the reader, so the author finds out when a human looks at the page - or
// never. This ran once over the repository after `class JI, SV gate;` (a space after the comma is
// not valid in a `class` statement) had been sitting in the README for a week.
//
// Exit codes: 0 everything parses, 1 at least one diagram does not, 2 the checker could not run.

import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');

const FENCE = /^(?:```|~~~)mermaid[^\n]*\n([\s\S]*?)^(?:```|~~~)[ \t]*$/gm;

function trackedFiles() {
  const out = execFileSync('git', ['ls-files', '-z'], { cwd: root, encoding: 'utf8' });
  return out.split('\0').filter((f) => f && /\.(md|mmd)$/i.test(f));
}

function blocks() {
  const found = [];
  for (const file of trackedFiles()) {
    const text = readFileSync(path.join(root, file), 'utf8');
    if (file.toLowerCase().endsWith('.mmd')) {
      found.push({ file, index: 0, text });
      continue;
    }
    FENCE.lastIndex = 0;
    let match;
    let n = 0;
    while ((match = FENCE.exec(text)) !== null) {
      n += 1;
      found.push({ file, index: n, text: match[1] });
    }
  }
  return found;
}

async function main() {
  const diagrams = blocks();
  if (diagrams.length === 0) {
    console.log('no mermaid diagrams found: nothing to check');
    return 0;
  }

  const { JSDOM } = await import('jsdom');
  const dom = new JSDOM('<!doctype html><html><body></body></html>', { pretendToBeVisual: true });
  // `navigator` is a getter on globalThis in Node 21+, so a plain assignment throws in ESM.
  const globals = {
    window: dom.window,
    document: dom.window.document,
    navigator: dom.window.navigator,
    Element: dom.window.Element,
    HTMLElement: dom.window.HTMLElement,
    SVGElement: dom.window.SVGElement,
  };
  for (const [key, value] of Object.entries(globals)) {
    try {
      globalThis[key] = value;
    } catch {
      Object.defineProperty(globalThis, key, { value, configurable: true, writable: true });
    }
  }

  const mermaid = (await import('mermaid')).default;
  mermaid.initialize({ startOnLoad: false, securityLevel: 'loose' });

  let failed = 0;
  for (const d of diagrams) {
    const label = d.index === 0 && d.file.endsWith('.mmd') ? d.file : `${d.file} (block ${d.index})`;
    try {
      const parsed = await mermaid.parse(d.text);
      console.log(`PASS ${label} -> ${parsed.diagramType}`);
    } catch (error) {
      failed += 1;
      const message = String(error?.message ?? error).split('\n').slice(0, 3).join(' | ');
      console.log(`FAIL ${label} -> ${message}`);
    }
  }

  console.log(`\n${diagrams.length - failed}/${diagrams.length} diagrams parse`);
  if (failed > 0) {
    console.log('GitHub would render the failing ones as "Unable to render rich display".');
    return 1;
  }
  return 0;
}

main()
  .then((code) => process.exit(code))
  .catch((error) => {
    console.error('mermaid check could not run:', error?.message ?? error);
    process.exit(2);
  });
