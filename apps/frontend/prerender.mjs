// Fills each public page's static HTML from the built page (dist/index.html) and the server render (src/prerender.tsx).
// dist/index.html itself stays the empty shell every other route loads.
import { readFileSync, rmSync, writeFileSync } from "node:fs";

const { pages, render } = await import("./.prerender/prerender.js");
const shell = readFileSync("dist/index.html", "utf8");
for (const [path, file] of Object.entries(pages)) {
  const html = shell.replace('<div id="root"></div>', `<div id="root">${render(path)}</div>`);
  if (html === shell) throw new Error("dist/index.html has no empty root to fill");
  writeFileSync(`dist/${file}`, html);
}
rmSync(".prerender", { recursive: true });
