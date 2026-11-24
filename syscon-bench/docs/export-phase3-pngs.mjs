#!/usr/bin/env node
/**
 * Renders phase3 HTML slides/readable pages to PNGs via Chrome CDP.
 * Slide panels use semi-transparent backgrounds; PNG omitBackground keeps opacity.
 */
import { spawn } from "node:child_process";
import { mkdir, writeFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const __dirname = path.dirname(fileURLToPath(import.meta.url));
const CHROME =
  process.env.CHROME ||
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const OUT = path.join(__dirname, "phase3-png");
const PORT = 9333;

function sleep(ms) {
  return new Promise((r) => setTimeout(r, ms));
}

async function cdp(method, params = {}, sessionId) {
  const body = { id: Date.now(), method, params };
  const headers = { "Content-Type": "application/json" };
  const url = sessionId
    ? `http://127.0.0.1:${PORT}/json`
    : `http://127.0.0.1:${PORT}/json/version`;
  // Use WebSocket via /json/list page targets — simpler fetch to page WS below.
  return body;
}

async function getWS() {
  const res = await fetch(`http://127.0.0.1:${PORT}/json/version`);
  const j = await res.json();
  return j.webSocketDebuggerUrl;
}

function wsRPC(wsUrl) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(wsUrl);
    let nextId = 1;
    const pending = new Map();
    ws.onopen = () => resolve({
      call(method, params = {}) {
        const id = nextId++;
        return new Promise((res, rej) => {
          pending.set(id, { res, rej });
          ws.send(JSON.stringify({ id, method, params }));
        });
      },
      close() { ws.close(); },
    });
    ws.onmessage = (ev) => {
      const msg = JSON.parse(ev.data);
      if (msg.id && pending.has(msg.id)) {
        const { res, rej } = pending.get(msg.id);
        pending.delete(msg.id);
        if (msg.error) rej(new Error(JSON.stringify(msg.error)));
        else res(msg.result);
      }
    };
    ws.onerror = reject;
  });
}

async function screenshotElement(pageWS, selector, outPath, { transparent }) {
  await pageWS.call("DOM.enable");
  await pageWS.call("Page.enable");
  await pageWS.call("Runtime.enable");
  const { root } = await pageWS.call("DOM.getDocument", { depth: 0 });
  const { nodeId } = await pageWS.call("DOM.querySelector", {
    nodeId: root.nodeId,
    selector,
  });
  if (!nodeId) throw new Error(`missing ${selector}`);
  const { model } = await pageWS.call("DOM.getBoxModel", { nodeId });
  const [x1, y1, , , x2, y2] = model.border;
  const clip = {
    x: Math.min(x1, x2),
    y: Math.min(y1, y2),
    width: Math.abs(x2 - x1),
    height: Math.abs(y2 - y1),
    scale: 1,
  };
  // pad a little
  clip.x = Math.max(0, clip.x - 8);
  clip.y = Math.max(0, clip.y - 8);
  clip.width += 16;
  clip.height += 16;
  const { data } = await pageWS.call("Page.captureScreenshot", {
    format: "png",
    fromSurface: true,
    omitBackground: transparent,
    clip,
    captureBeyondViewport: true,
  });
  await writeFile(outPath, Buffer.from(data, "base64"));
  console.log("wrote", outPath);
}

async function openPage(browserWS, fileUrl) {
  const { targetId } = await browserWS.call("Target.createTarget", {
    url: fileUrl,
  });
  const { sessionId } = await browserWS.call("Target.attachToTarget", {
    targetId,
    flatten: true,
  });
  // flattened sessions use same WS with sessionId — Chrome expects sessionId field
  const call = (method, params = {}) =>
    browserWS.callRaw
      ? browserWS.callRaw(method, params, sessionId)
      : browserWS.call(method, params); // fallback
  return { targetId, sessionId, call };
}

// Proper flattened CDP over one WS
function wsRPCSession(wsUrl) {
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(wsUrl);
    let nextId = 1;
    const pending = new Map();
    ws.onopen = () => {
      const api = {
        call(method, params = {}, sessionId) {
          const id = nextId++;
          const msg = { id, method, params };
          if (sessionId) msg.sessionId = sessionId;
          return new Promise((res, rej) => {
            pending.set(id, { res, rej });
            ws.send(JSON.stringify(msg));
          });
        },
        close() {
          ws.close();
        },
      };
      resolve(api);
    };
    ws.onmessage = (ev) => {
      const msg = JSON.parse(ev.data);
      if (msg.id && pending.has(msg.id)) {
        const { res, rej } = pending.get(msg.id);
        pending.delete(msg.id);
        if (msg.error) rej(new Error(JSON.stringify(msg.error)));
        else res(msg.result);
      }
    };
    ws.onerror = reject;
  });
}

async function main() {
  await mkdir(OUT, { recursive: true });
  const chrome = spawn(
    CHROME,
    [
      `--remote-debugging-port=${PORT}`,
      "--headless=new",
      "--disable-gpu",
      "--hide-scrollbars",
      "--default-background-color=00000000",
      "about:blank",
    ],
    { stdio: ["ignore", "ignore", "ignore"] }
  );
  let ready = false;
  for (let i = 0; i < 40; i++) {
    await sleep(250);
    try {
      const r = await fetch(`http://127.0.0.1:${PORT}/json/version`);
      if (r.ok) {
        ready = true;
        break;
      }
    } catch {
      /* retry */
    }
  }
  if (!ready) throw new Error("Chrome CDP did not become ready");
  try {
    const ver = await fetch(`http://127.0.0.1:${PORT}/json/version`).then((r) =>
      r.json()
    );
    const browser = await wsRPCSession(ver.webSocketDebuggerUrl);

    const slidesUrl = pathToFileURL(path.join(__dirname, "phase3-slides.html")).href;
    const readableUrl = pathToFileURL(
      path.join(__dirname, "phase3-readable.html")
    ).href;

    async function withPage(url, fn) {
      const { targetId } = await browser.call("Target.createTarget", { url });
      const { sessionId } = await browser.call("Target.attachToTarget", {
        targetId,
        flatten: true,
      });
      const page = {
        call: (method, params = {}) => browser.call(method, params, sessionId),
      };
      await page.call("Page.enable");
      await page.call("DOM.enable");
      await page.call("Page.setDeviceMetricsOverride", {
        width: 1720,
        height: 2400,
        deviceScaleFactor: 2,
        mobile: false,
      });
      // wait for load
      await sleep(800);
      try {
        await fn(page);
      } finally {
        await browser.call("Target.closeTarget", { targetId });
      }
    }

    const slideIds = [
      "slide-headline",
      "slide-ingest",
      "slide-resources",
      "slide-query",
      "slide-disk-measured",
      "slide-disk-scaled",
    ];

    await withPage(slidesUrl, async (page) => {
      // expand viewport for tall page
      const { root } = await page.call("DOM.getDocument", { depth: 0 });
      for (const id of slideIds) {
        await screenshotElement(page, `#${id}`, path.join(OUT, `${id}.png`), {
          transparent: true,
        });
      }
    });

    await withPage(readableUrl, async (page) => {
      await page.call("Page.setDeviceMetricsOverride", {
        width: 1200,
        height: 3200,
        deviceScaleFactor: 2,
        mobile: false,
      });
      await sleep(400);
      await screenshotElement(page, "#readable", path.join(OUT, "phase3-readable.png"), {
        transparent: false,
      });
    });

    // also full-page opaque slides strip for convenience
    await withPage(slidesUrl, async (page) => {
      await page.call("Page.setDeviceMetricsOverride", {
        width: 1720,
        height: 6200,
        deviceScaleFactor: 2,
        mobile: false,
      });
      await sleep(500);
      const { data } = await page.call("Page.captureScreenshot", {
        format: "png",
        fromSurface: true,
        omitBackground: true,
        captureBeyondViewport: true,
      });
      await writeFile(path.join(OUT, "phase3-slides-all.png"), Buffer.from(data, "base64"));
      console.log("wrote", path.join(OUT, "phase3-slides-all.png"));
    });

    browser.close();
  } finally {
    chrome.kill("SIGTERM");
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
