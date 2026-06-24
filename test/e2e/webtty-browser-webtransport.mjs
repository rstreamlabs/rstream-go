#!/usr/bin/env node
// See LICENSE file in the project root for license information.

import { createHash } from "node:crypto";
import { createServer } from "node:http";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { spawn } from "node:child_process";
import { tmpdir } from "node:os";
import net from "node:net";

const scriptDir = dirname(fileURLToPath(import.meta.url));
const root = join(scriptDir, "../..");
const jsRoot = process.env.RSTREAM_JS_REPO ?? join(root, "..", "rstream-js");
const chromeBin =
  process.env.RSTREAM_WEBTTY_CHROME_BIN ??
  "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const timeoutMs = Number(process.env.RSTREAM_WEBTTY_RUNTIME_TIMEOUT_SECONDS ?? "30") * 1000;

function spawnLogged(cmd, args, opts = {}) {
  return spawn(cmd, args, { stdio: ["ignore", "pipe", "pipe"], ...opts });
}

function waitExit(child, label) {
  return new Promise((resolve, reject) => {
    let stdout = "";
    let stderr = "";
    child.stdout?.on("data", (chunk) => {
      stdout += chunk;
    });
    child.stderr?.on("data", (chunk) => {
      stderr += chunk;
    });
    child.on("error", reject);
    child.on("exit", (code) => {
      if (code === 0) resolve({ stdout, stderr });
      else reject(new Error(`${label} exited ${code}\n${stdout}\n${stderr}`));
    });
  });
}

function waitChildExit(child) {
  return new Promise((resolve) => {
    if (child.exitCode !== null || child.killed) {
      resolve();
      return;
    }
    child.once("exit", () => resolve());
    setTimeout(resolve, 3000);
  });
}

function reservePort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.listen(0, "127.0.0.1", () => {
      const port = server.address().port;
      server.close(() => resolve(port));
    });
    server.on("error", reject);
  });
}

async function fetchJSON(url, opts) {
  const response = await fetch(url, opts);
  if (!response.ok) throw new Error(`${response.status} ${await response.text()}`);
  return await response.json();
}

async function waitChrome(remotePort) {
  const deadline = Date.now() + timeoutMs;
  let last = "";
  while (Date.now() < deadline) {
    try {
      return await fetchJSON(`http://127.0.0.1:${remotePort}/json/version`);
    } catch (error) {
      last = error instanceof Error ? error.message : String(error);
    }
    await new Promise((resolve) => setTimeout(resolve, 150));
  }
  throw new Error(`Chrome DevTools did not become ready: ${last}`);
}

function cdpClient(wsURL) {
  let nextID = 1;
  const pending = new Map();
  const ws = new WebSocket(wsURL);
  ws.addEventListener("message", (event) => {
    const msg = JSON.parse(event.data);
    if (!msg.id || !pending.has(msg.id)) return;
    const { resolve, reject } = pending.get(msg.id);
    pending.delete(msg.id);
    if (msg.error) reject(new Error(JSON.stringify(msg.error)));
    else resolve(msg.result);
  });
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("CDP websocket timeout")), 10000);
    ws.addEventListener("open", () => {
      clearTimeout(timer);
      resolve({
        close() {
          ws.close();
        },
        send(method, params = {}) {
          const id = nextID++;
          ws.send(JSON.stringify({ id, method, params }));
          return new Promise((resolve, reject) => pending.set(id, { resolve, reject }));
        },
      });
    });
    ws.addEventListener("error", reject);
  });
}

async function newPage(remotePort) {
  await waitChrome(remotePort);
  const target = await fetchJSON(`http://127.0.0.1:${remotePort}/json/new?about:blank`, {
    method: "PUT",
  });
  return cdpClient(target.webSocketDebuggerUrl);
}

function pemToDer(pem) {
  const text = pem.toString("utf8");
  const body = text
    .replace(/-----BEGIN CERTIFICATE-----/g, "")
    .replace(/-----END CERTIFICATE-----/g, "")
    .replace(/\s+/g, "");
  return Buffer.from(body, "base64");
}

async function buildRstream(tmp) {
  if (process.env.RSTREAM_BIN) return process.env.RSTREAM_BIN;
  const out = join(tmp, "rstream");
  await waitExit(spawnLogged("go", ["build", "-o", out, "./cmd/rstream"], { cwd: root }), "go build");
  return out;
}

async function main() {
  const tmp = await mkdtemp(join(tmpdir(), "rstream-webtty-browser-wt-"));
  const children = [];
  let server;
  let page;
  try {
    const rstream = await buildRstream(tmp);
    const bundleDir = join(tmp, "bundle");
    await waitExit(
      spawnLogged(
        "node",
        [
          "node_modules/tsup/dist/cli-default.js",
          "packages/webtty/src/index.ts",
          "--format",
          "esm",
          "--platform",
          "browser",
          "--no-splitting",
          "--out-dir",
          bundleDir,
          "--clean",
          "--no-config",
          "--target",
          "es2022",
          "--silent",
        ],
        { cwd: jsRoot },
      ),
      "tsup browser bundle",
    );
    const browserBundle = await readFile(join(bundleDir, "index.mjs"));
    const cert = join(tmp, "webtty.crt");
    const key = join(tmp, "webtty.key");
    await waitExit(
      spawnLogged("openssl", ["ecparam", "-name", "prime256v1", "-genkey", "-noout", "-out", key]),
      "openssl ecparam",
    );
    await waitExit(
      spawnLogged("openssl", [
        "req",
        "-new",
        "-x509",
        "-key",
        key,
        "-out",
        cert,
        "-days",
        "13",
        "-subj",
        "/CN=localhost",
        "-addext",
        "subjectAltName=DNS:localhost,IP:127.0.0.1",
        "-addext",
        "keyUsage=digitalSignature",
        "-addext",
        "extendedKeyUsage=serverAuth",
      ]),
      "openssl cert",
    );
    const certHash = [
      ...createHash("sha256").update(pemToDer(await readFile(cert))).digest(),
    ];
    const wtPort = await reservePort();
    const pagePort = await reservePort();
    const pageOrigin = `http://127.0.0.1:${pagePort}`;
    const wtURL = `https://127.0.0.1:${wtPort}/`;
    const webtty = spawnLogged(
      rstream,
      [
        "webtty",
        "server",
        "--listen",
        `127.0.0.1:${wtPort}`,
        "--transport",
        "webtransport",
        "--allow-unauthenticated",
        "--allowed-origin",
        pageOrigin,
        "--tls-cert-file",
        cert,
        "--tls-key-file",
        key,
      ],
      { cwd: root },
    );
    children.push(webtty);
    await new Promise((resolve) => setTimeout(resolve, 1200));
    server = createServer((req, res) => {
      if (req.url === "/dist/index.mjs") {
        res.writeHead(200, {
          "access-control-allow-origin": "*",
          "content-type": "text/javascript",
        });
        res.end(browserBundle);
        return;
      }
      res.writeHead(200, { "content-type": "text/html" });
      res.end("<!doctype html><meta charset=\"utf-8\"><title>webtty wt runtime</title>");
    });
    await new Promise((resolve) => server.listen(pagePort, "127.0.0.1", resolve));
    const remotePort = await reservePort();
    const chrome = spawn(
      chromeBin,
      [
        "--headless=new",
        "--disable-gpu",
        "--no-first-run",
        "--no-default-browser-check",
        `--user-data-dir=${join(tmp, "chrome-profile")}`,
        `--remote-debugging-port=${remotePort}`,
        "--enable-experimental-web-platform-features",
        `--origin-to-force-quic-on=127.0.0.1:${wtPort}`,
        "about:blank",
      ],
      { stdio: ["ignore", "ignore", "pipe"] },
    );
    children.push(chrome);
    page = await newPage(remotePort);
    await page.send("Runtime.enable");
    await page.send("Page.enable");
    await page.send("Page.navigate", { url: `${pageOrigin}/` });
    await new Promise((resolve) => setTimeout(resolve, 1000));
    const expression = `(async () => {
      if (typeof WebTransport === "undefined") throw new Error("WebTransport unavailable");
      const mod = await import("${pageOrigin}/dist/index.mjs");
      const result = await mod.runWebTTYCommand(
        {
          sendHeartbeat: false,
          transport: "webtransport",
          url: "${wtURL}",
          webTransportOptions: {
            serverCertificateHashes: [
              { algorithm: "sha-256", value: new Uint8Array(${JSON.stringify(certHash)}) },
            ],
          },
        },
        "sh",
        ["-lc", "printf js-webtransport-browser"],
        { timeoutMs: 15000 },
      );
      if (!result.success || result.stdout !== "js-webtransport-browser" || result.stderr !== "") {
        throw new Error("unexpected result " + JSON.stringify(result));
      }
      return result;
    })()`;
    const result = await page.send("Runtime.evaluate", {
      awaitPromise: true,
      expression,
      returnByValue: true,
    });
    if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
    console.log(JSON.stringify(result.result.value));
  } finally {
    try {
      page?.close?.();
    } catch {
      // best effort
    }
    if (server) await new Promise((resolve) => server.close(resolve));
    for (const child of children.reverse()) {
      if (child.exitCode === null) child.kill("SIGTERM");
    }
    await Promise.all(children.map(waitChildExit));
    await rm(tmp, { force: true, maxRetries: 5, recursive: true, retryDelay: 200 });
  }
}

main().catch((error) => {
  console.error(error instanceof Error ? error.stack : error);
  process.exit(1);
});
