import { writeFile } from "node:fs/promises";
import path from "node:path";
import process from "node:process";

// A nested module boundary keeps `go list ./...` from traversing JavaScript
// packages that happen to contain Go source files.
await writeFile(
  path.join(process.cwd(), "node_modules/go.mod"),
  "module rionlyu.dev/frontend-dependencies\n\ngo 1.26.0\n",
);
