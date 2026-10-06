import { createRequire } from 'node:module';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';

// Reuse the dashboard dependency without introducing a separate asset package.
const require = createRequire(new URL('../../../apps/dashboard/package.json', import.meta.url));
const sharp = require('sharp');
const manifest = JSON.parse(await readFile(new URL('./manifest.json', import.meta.url), 'utf8'));

for (const icon of manifest.icons) {
  await sharp(fileURLToPath(new URL(icon.svg, import.meta.url)))
    .resize(manifest.discordSize, manifest.discordSize)
    .png()
    .toFile(fileURLToPath(new URL(icon.png, import.meta.url)));
}

console.log(`Exported ${manifest.icons.length} local PNGs. No uploads performed.`);
