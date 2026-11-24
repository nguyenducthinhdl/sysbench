#!/usr/bin/env node
/**
 * Export phase3 slide / readable HTML to PNGs via headless Chrome --screenshot.
 * Slide pages use a transparent body so PNG backgrounds stay clear for decks.
 */
import { spawn } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const CHROME =
  process.env.CHROME ||
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const OUT = path.join(__dirname, "phase3-png");

const SLIDES = [
  "slide-headline",
  "slide-ingest",
  "slide-resources",
  "slide-query",
  "slide-disk-measured",
  "slide-disk-scaled",
];

function run(cmd, args, opts = {}) {
  return new Promise((resolve, reject) => {
    const child = spawn(cmd, args, {
      stdio: ["ignore", "pipe", "pipe"],
      ...opts,
    });
    let stderr = "";
    child.stderr.on("data", (d) => {
      stderr += d.toString();
    });
    const timer = setTimeout(() => {
      child.kill("SIGKILL");
      reject(new Error(`timeout: ${cmd} ${args.join(" ")}\n${stderr}`));
    }, opts.timeoutMs || 45000);
    child.on("error", reject);
    child.on("close", (code) => {
      clearTimeout(timer);
      if (code === 0) resolve(stderr);
      else reject(new Error(`exit ${code}\n${stderr}`));
    });
  });
}

async function screenshot(htmlPath, pngPath, { width, height }) {
  await run(CHROME, [
    "--headless=new",
    "--disable-gpu",
    "--hide-scrollbars",
    "--no-first-run",
    "--no-default-browser-check",
    "--default-background-color=00000000",
    `--window-size=${width},${height}`,
    `--screenshot=${pngPath}`,
    pathToFileURL(htmlPath).href,
  ]);
  const st = await stat(pngPath);
  console.log(
    "wrote",
    pngPath,
    `(${Math.round(st.size / 1024)} KB, ${width}x${height})`
  );
}

async function buildSlidePages(tmp, src) {
  const style = src.match(/<style>[\s\S]*?<\/style>/)[0];
  const script = src.match(/<script>[\s\S]*?<\/script>/)[0];
  const paths = [];
  for (const id of SLIDES) {
    const re = new RegExp(
      `<section class="slide" id="${id}">[\\s\\S]*?<\\/section>`
    );
    const section = src.match(re)?.[0];
    if (!section) throw new Error(`section ${id} not found`);
    const html = `<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8"/>${style}
<style>
  html, body { margin: 0; background: transparent !important; }
  body { width: 1600px; }
  .slide { margin: 0 !important; width: 1600px; min-height: 0 !important; height: auto !important; }
</style></head><body>
${section}
${script}
</body></html>`;
    const p = path.join(tmp, `${id}.html`);
    await writeFile(p, html);
    paths.push({ id, path: p });
  }
  return paths;
}

/** Trim transparent / near-empty padding so slides sit tight for Keynote. */
async function cropPng(pngPath, { pad = 6, alphaThresh = 10 } = {}) {
  // Prefer sips on macOS for speed when available; fall back to keeping as-is.
  // Content-aware crop via a tiny inline decoder is done in crop-phase3-pngs.py if present.
  const helper = path.join(__dirname, "crop-phase3-pngs.py");
  try {
    await run("python3", [helper, pngPath, String(pad), String(alphaThresh)], {
      timeoutMs: 60000,
    });
  } catch (err) {
    console.warn("crop skipped:", err.message.split("\n")[0]);
  }
}

async function main() {
  await mkdir(OUT, { recursive: true });
  const tmp = await mkdtemp(path.join(tmpdir(), "phase3-png-"));
  try {
    const src = await readFile(
      path.join(__dirname, "phase3-slides.html"),
      "utf8"
    );
    const slides = await buildSlidePages(tmp, src);
    for (const s of slides) {
      const png = path.join(OUT, `${s.id}.png`);
      await screenshot(s.path, png, { width: 1632, height: 1200 });
      await cropPng(png);
    }

    // Readable: white page, taller window
    const readable = path.join(OUT, "phase3-readable.png");
    await screenshot(path.join(__dirname, "phase3-readable.html"), readable, {
      width: 1200,
      height: 3200,
    });
    await cropPng(readable, { pad: 12 });
  } finally {
    await rm(tmp, { recursive: true, force: true });
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
