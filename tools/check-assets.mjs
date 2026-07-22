import { createHash } from "node:crypto";
import { readFile } from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import { gunzipSync } from "node:zlib";

const root = process.cwd();

export function validateDuck(source) {
  const lower = source.toLowerCase();
  for (const forbidden of ["<script", "<image", "<foreignobject", "<iframe", "<object", "<use", "href=", "onload=", "javascript:"]) {
    if (lower.includes(forbidden)) throw new Error(`duck SVG contains forbidden content: ${forbidden}`);
  }
  const checksum = createHash("sha256").update(source).digest("hex");
  const expected = "dada0715179ef9cfc6467993c37509b312ad2c68f0b1e958885e1c2f2dc1c7a1";
  if (checksum !== expected) {
    throw new Error(`duck SVG checksum ${checksum} does not match pinned Noto asset ${expected}`);
  }
}

export function digest(content) {
  return createHash("sha256").update(content).digest("hex").slice(0, 12);
}

async function checkBuiltAssets() {
  const source = await readFile(path.join(root, "web/assets/src/duck-icon.svg"), "utf8");
  validateDuck(source);
  const manifestPath = path.join(root, "web/assets/dist/manifest.json");
  const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
  const required = ["site.css", "titles.js", "contact.js", "duck-icon.svg", "favicon.svg"];
  for (const logicalName of required) {
    const fileName = manifest[logicalName];
    if (!fileName) throw new Error(`asset manifest is missing ${logicalName}`);
    const content = await readFile(path.join(root, "web/assets/dist", fileName));
    if (!fileName.includes(digest(content))) throw new Error(`${fileName} does not contain its content hash`);
    const compressed = await readFile(path.join(root, "web/assets/dist", `${fileName}.gz`));
    if (compressed[9] !== 255) throw new Error(`${fileName}.gz records a host-specific gzip OS`);
    if (!gunzipSync(compressed).equals(content)) throw new Error(`${fileName}.gz does not expand to the fingerprinted asset`);
  }
}

if (process.argv[1] && path.resolve(process.argv[1]) === path.resolve(new URL(import.meta.url).pathname)) {
  await checkBuiltAssets();
}
