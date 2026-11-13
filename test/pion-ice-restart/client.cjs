// See LICENSE file in the project root for license information.

const assert = require('node:assert/strict');
const { spawn } = require('node:child_process');
const { access } = require('node:fs/promises');
const { createInterface } = require('node:readline');
const { setTimeout: delay } = require('node:timers/promises');
const { RTCPeerConnection } = require('@roamhq/wrtc');

async function owner(executable, env, context, children) {
  await access(executable);
  context.signal.throwIfAborted();
  const child = spawn(executable, [], { env, stdio: ['ignore', 'pipe', 'inherit'] });
  const lines = createInterface({ input: child.stdout });
  const closed = new Promise(resolve => child.once('close', resolve));
  children.push({ child, lines, closed });
  const readiness = new Promise((resolve, reject) => {
    const abort = () => reject(context.signal.reason);
    const finish = () => context.signal.removeEventListener('abort', abort);
    lines.once('line', line => { finish(); resolve(JSON.parse(line)); });
    child.once('error', error => { finish(); reject(error); });
    child.once('close', () => { finish(); reject(new Error('owner exited')); });
    context.signal.addEventListener('abort', abort, { once: true });
    if (context.signal.aborted) abort();
  });
  return readiness;
}

function until(eventTarget, event, predicate, signal) {
  signal.throwIfAborted();
  if (predicate()) return Promise.resolve();
  return new Promise((resolve, reject) => {
    const finish = () => {
      eventTarget.removeEventListener(event, changed);
      signal.removeEventListener('abort', abort);
    };
    const changed = () => { if (predicate()) { finish(); resolve(); } };
    const abort = () => { finish(); reject(signal.reason); };
    eventTarget.addEventListener(event, changed);
    signal.addEventListener('abort', abort, { once: true });
    changed();
    if (signal.aborted) abort();
  });
}

async function run() {
  const context = new AbortController();
  const deadline = setTimeout(() => context.abort(new Error('public Pion transfer deadline')), 30000);
  const children = [];
  const state = { done: false, restarts: 0, queue: [], wake: undefined, maxQueue: 0, consumed: 0, peer: undefined };
  const monitor = setInterval(() => console.log(JSON.stringify({ consumed: state.consumed, restarts: state.restarts, queue: state.queue.length, ice: state.peer?.iceConnectionState })), 1000);
  try {
    await Promise.race([(async () => {
    const relay = process.env.TURN_SERVER
      ? await owner(process.env.TURN_SERVER, { ...process.env, TURN_ONLY: '1' }, context, children)
      : undefined;
    const info = await owner(process.env.SERVER, { ...process.env, ...(relay ? { EXTERNAL_TURN: relay.turn } : {}) }, context, children);
    const peer = new RTCPeerConnection({ iceServers: info.iceServers, iceTransportPolicy: 'relay' });
    state.peer = peer;
    const channel = peer.createDataChannel('probe');
    channel.binaryType = 'arraybuffer';
    channel.onmessage = event => {
      try {
        state.queue.push(Buffer.from(event.data));
        state.maxQueue = Math.max(state.maxQueue, state.queue.length);
        assert.ok(state.queue.length <= 32, 'receive credit window exceeded');
        state.wake?.();
      } catch (error) { context.abort(error); }
    };
    async function negotiate(restart) {
      await peer.setLocalDescription(await peer.createOffer({ iceRestart: restart }));
      await until(peer, 'icegatheringstatechange', () => peer.iceGatheringState === 'complete', context.signal);
      const response = await fetch(info.url, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(peer.localDescription), signal: context.signal });
      assert.equal(response.status, 200, await response.clone().text());
      await peer.setRemoteDescription(await response.json());
    }
    await negotiate(false);
    await until(channel, 'open', () => channel.readyState === 'open', context.signal);
    const restarting = (async () => {
      while (!state.done) {
        await delay(2000, null, { signal: context.signal });
        if (state.done) return;
        await negotiate(true);
        state.restarts++;
      }
    })();
    void restarting.catch(error => { if (!state.done) context.abort(error); });
    for (const index of Array.from({ length: 256 }, (_, i) => i)) {
      if (!state.queue.length) await new Promise((resolve, reject) => {
        const finish = () => { context.signal.removeEventListener('abort', abort); state.wake = undefined; };
        const abort = () => { finish(); reject(context.signal.reason); };
        state.wake = () => { finish(); resolve(); };
        context.signal.addEventListener('abort', abort, { once: true });
        if (context.signal.aborted) abort();
      });
      const bytes = state.queue.shift();
      assert.equal(bytes.length, 32768);
      assert.equal(bytes.readUInt32BE(), index);
      assert.ok(bytes.subarray(4).every(value => value === index % 251));
      await delay(25, null, { signal: context.signal });
      channel.send('credit');
      state.consumed = index + 1;
    }
    state.done = true;
    assert.ok(state.restarts > 0);
    console.log(JSON.stringify({ pass: true, bytes: 256 * 32768, restarts: state.restarts, maxQueue: state.maxQueue }));
    })(), new Promise((_, reject) => context.signal.addEventListener("abort", () => reject(context.signal.reason), { once: true }))]);
  } finally {
    state.done = true;
    clearTimeout(deadline);
    clearInterval(monitor);
    context.abort();
    state.peer?.close();
    for (const { child, lines, closed } of children.reverse()) {
      child.kill('SIGTERM');
      const killed = setTimeout(() => child.kill('SIGKILL'), 5000);
      const code = await closed;
      clearTimeout(killed);
      assert.equal(code, 0, "owned Pion process failed or required SIGKILL");
      lines.close();
    }
  }
}

void run().catch(error => { console.error(error); process.exitCode = 1; });
