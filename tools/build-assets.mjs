import { mkdir, readFile, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import process from "node:process";
import { gzipSync } from "node:zlib";
import { build } from "esbuild";
import { digest, validateDuck } from "./check-assets.mjs";

const root = process.cwd();
const sourceDirectory = path.join(root, "web/assets/src");
const outputDirectory = path.join(root, "web/assets/dist");
const manifest = {};

await rm(outputDirectory, { recursive: true, force: true });
await mkdir(outputDirectory, { recursive: true });

async function emit(logicalName, content) {
  const extension = path.extname(logicalName);
  const base = path.basename(logicalName, extension);
  const fileName = `${base}.${digest(content)}${extension}`;
  await writeFile(path.join(outputDirectory, fileName), content);
  await writeFile(path.join(outputDirectory, `${fileName}.gz`), gzipSync(content, { level: 9 }));
  manifest[logicalName] = fileName;
}

async function bundleScript(logicalName, input) {
  const result = await build({
    entryPoints: [path.join(sourceDirectory, input)],
    bundle: true,
    write: false,
    minify: true,
    format: "iife",
    platform: "browser",
    target: ["es2022"],
    legalComments: "none",
  });
  await emit(logicalName, result.outputFiles[0].contents);
}

const cssResult = await build({
  entryPoints: [path.join(sourceDirectory, "site.css")],
  bundle: true,
  write: false,
  minify: true,
  target: ["es2022"],
  legalComments: "none",
});
await emit("site.css", cssResult.outputFiles[0].contents);
await bundleScript("titles.js", "titles.js");
await bundleScript("contact.js", "contact.js");

const duck = await readFile(path.join(sourceDirectory, "duck-icon.svg"));
validateDuck(duck.toString("utf8"));
await emit("duck-icon.svg", duck);
await emit("favicon.svg", await readFile(path.join(sourceDirectory, "favicon.svg")));

const sortedManifest = Object.fromEntries(Object.entries(manifest).sort(([left], [right]) => left.localeCompare(right)));
await writeFile(path.join(outputDirectory, "manifest.json"), `${JSON.stringify(sortedManifest, null, 2)}\n`);
