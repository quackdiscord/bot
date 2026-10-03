import { readFile, writeFile } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import { fileURLToPath } from 'node:url';

// Compile approved upload IDs into the backend without runtime asset reads or API calls.
// Run this after recording new uploads in manifest.json; it never uploads icons.
const manifest = JSON.parse(await readFile(new URL('./manifest.json', import.meta.url), 'utf8'));
const applications = new Map();
for (const icon of manifest.icons) {
  for (const [appId, emojiId] of Object.entries(icon.emojiIds ?? {})) {
    if (!/^\d+$/.test(appId) || !/^\d+$/.test(emojiId) || !/^[a-z_]+$/.test(icon.key) || !/^\w+$/.test(icon.name)) {
      throw new Error(`Invalid uploaded emoji identity for ${icon.key}`);
    }
    if (!applications.has(appId)) applications.set(appId, []);
    applications.get(appId).push(`\t\t${JSON.stringify(icon.key)}: ${JSON.stringify(`<:${icon.name}:${emojiId}>`)},`);
  }
}
const target = fileURLToPath(new URL('../../../apps/backend/internal/discordtext/icons_generated.go', import.meta.url));
const source = '// Code generated from assets/icons/quack/manifest.json; DO NOT EDIT.\n\npackage discordtext\n\n' +
  '// applicationIcons contains the approved emoji uploads for each bot identity.\nvar applicationIcons = map[string]map[string]string{\n' +
  [...applications].map(([id, lines]) => `\t${JSON.stringify(id)}: {\n${lines.join('\n')}\n\t},`).join('\n') + '\n}\n';
await writeFile(target, source);
execFileSync('gofmt', ['-w', target]);
console.log(`Generated catalogs for ${applications.size} applications. No uploads performed.`);
