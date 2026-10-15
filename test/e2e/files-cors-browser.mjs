// See LICENSE file in the project root for license information.

import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";

const [filesURL, dashboardURL, untrustedURL] = process.argv.slice(2);
assert.ok(filesURL && dashboardURL && untrustedURL, "expected files, dashboard and untrusted URLs");
const chrome = process.env.RSTREAM_CHROME_BIN ?? "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome";
const profile = await mkdtemp(join(tmpdir(), "rstream-cors-browser-"));
const certificateArgs = process.env.RSTREAM_CORS_TEST_SPKI ? [`--ignore-certificate-errors-spki-list=${process.env.RSTREAM_CORS_TEST_SPKI}`] : [];
const child = spawn(chrome, [...certificateArgs, "--headless=new", "--no-first-run", "--no-default-browser-check", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "about:blank"], { stdio: "ignore" });
const exit = new Promise((resolve, reject) => {
  child.once("exit", resolve);
  child.once("error", reject);
});
exit.catch(() => {});
const deadline = Date.now() + 30000;
async function untilReady(operation) {
  while (Date.now() < deadline) {
    try {
      const value = await operation();
      if (value) return value;
    } catch (error) {
      if (child.exitCode !== null) throw error;
    }
    await delay(50);
  }
  throw new Error("browser readiness deadline exceeded");
}
function connect(url) {
  const socket = new WebSocket(url);
  const pending = new Map();
  let sequence = 0;
  socket.addEventListener("message", (event) => {
    const message = JSON.parse(event.data);
    const operation = pending.get(message.id);
    if (!operation) return;
    pending.delete(message.id);
    if (message.error) operation.reject(new Error(JSON.stringify(message.error)));
    else operation.resolve(message.result);
  });
  return new Promise((resolve, reject) => {
    socket.addEventListener("error", reject, { once: true });
    socket.addEventListener("open", () => resolve({
      close: () => socket.close(),
      send: (method, params = {}, sessionId) => new Promise((resolve, reject) => {
        const id = ++sequence;
        pending.set(id, { resolve, reject });
        socket.send(JSON.stringify({ id, method, params, sessionId }));
      }),
    }), { once: true });
  });
}
let cdp;
try {
  const port = await untilReady(async () => (await readFile(join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0]);
  const version = await (await fetch(`http://127.0.0.1:${port}/json/version`)).json();
  cdp = await connect(version.webSocketDebuggerUrl);
  const { targetId } = await cdp.send("Target.createTarget", { url: "about:blank" });
  const { sessionId } = await cdp.send("Target.attachToTarget", { targetId, flatten: true });
  const evaluate = async (expression) => {
    const value = await cdp.send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true }, sessionId);
    assert.equal(value.exceptionDetails, undefined, JSON.stringify(value.exceptionDetails));
    return value.result.value;
  };
  const navigate = async (url) => {
    await cdp.send("Page.navigate", { url }, sessionId);
    await untilReady(async () => (await evaluate("location.origin")) === url);
  };
  await navigate(dashboardURL);
  const results = await evaluate(`(async () => {
    const base = ${JSON.stringify(filesURL)};
    const token = ${JSON.stringify(process.env.RSTREAM_CORS_TEST_TOKEN ?? "")};
    const queryToken = ${JSON.stringify(process.env.RSTREAM_CORS_TEST_AUTH === "query")};
    const authorization = token ? 'Bearer ' + token : 'Basic ' + btoa('rstream:browser-test-only');
    const request = async (path, options = {}) => {
      const target = new URL(base + path);
      if (queryToken && options.headers?.Authorization) {
        target.searchParams.set('rstream.token', options.headers.Authorization.slice('Bearer '.length));
        delete options.headers.Authorization;
      }
      const response = await fetch(target, {credentials: 'omit', ...options});
      return {status: response.status, body: await response.text(), range: response.headers.get('Content-Range'), disposition: response.headers.get('Content-Disposition')};
    };
    return {
      unauthenticated: await request('/_rstream/files/v1/info'),
      info: await request('/_rstream/files/v1/info', {headers: {Authorization: authorization}}),
      listing: await request('/fs/', {method: 'PROPFIND', headers: {Authorization: authorization, Depth: '1'}}),
      upload: await request('/fs/uploaded.txt', {method: 'PUT', headers: {Authorization: authorization}, body: 'browser upload'}),
      download: await request('/fs/uploaded.txt', {headers: {Authorization: authorization}}),
      partial: await request('/fs/hello.txt', {headers: {Authorization: authorization, Range: 'bytes=1-4'}}),
      missing: await request('/fs/missing.txt', {headers: {Authorization: authorization}}),
      expired: token ? await request('/_rstream/files/v1/info', {headers: {Authorization: 'Bearer ' + ${JSON.stringify(process.env.RSTREAM_CORS_TEST_EXPIRED ?? "invalid")}}}) : null,
      wrongScope: token ? await request('/_rstream/files/v1/info', {headers: {Authorization: 'Bearer ' + ${JSON.stringify(process.env.RSTREAM_CORS_TEST_WRONG_SCOPE ?? "invalid")}}}) : null,
    };
  })()`);
  assert.equal(results.unauthenticated.status, 401);
  assert.equal(results.info.status, 200);
  assert.equal(results.listing.status, 207);
  assert.equal(results.upload.status, 201);
  assert.equal(results.download.body, "browser upload");
  assert.equal(results.partial.status, 206);
  assert.equal(results.partial.body, "ello");
  assert.equal(results.partial.range, "bytes 1-4/11");
  assert.ok(results.partial.disposition);
  assert.equal(results.missing.status, 404);
  if (process.env.RSTREAM_CORS_TEST_TOKEN) {
    assert.equal(results.expired.status, 401);
    assert.equal(results.wrongScope.status, 403);
  }
  await navigate(untrustedURL);
  const blocked = await evaluate(`(async () => {
    try {
      const token = ${JSON.stringify(process.env.RSTREAM_CORS_TEST_TOKEN ?? "")};
      const queryToken = ${JSON.stringify(process.env.RSTREAM_CORS_TEST_AUTH === "query")};
      const target = new URL(${JSON.stringify(filesURL)} + '/fs/hello.txt');
      if (queryToken) target.searchParams.set('rstream.token', token);
      const headers = queryToken ? {} : {Authorization: token ? 'Bearer ' + token : 'Basic ' + btoa('rstream:browser-test-only')};
      await fetch(target, {method: 'PUT', credentials: 'omit', headers, body: 'forbidden'});
      return false;
    } catch (error) { return error instanceof TypeError; }
  })()`);
  assert.equal(blocked, true);
  console.log("Browser CORS: discovery, WebDAV listing, upload, download, range, errors and rejected origin passed");
} finally {
  cdp?.close();
  child.kill("SIGTERM");
  await Promise.race([exit.catch(() => {}), delay(2000)]);
  if (child.exitCode === null) child.kill("SIGKILL");
  await rm(profile, { recursive: true, force: true });
}
