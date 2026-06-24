#!/usr/bin/env node
// Smoke test: simulates iOS wire protocol against go-bridge.
// Usage: node test-smoke.mjs (starts go-bridge automatically on port 8767)

import { WebSocket } from 'ws';

const PORT = process.env.PORT || 8767;

function send(ws, msg) {
  const json = JSON.stringify(msg);
  console.log(`→ ${json}`);
  ws.send(json);
}

function waitFor(ws, predicate, timeout = 10000) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      reject(new Error('timeout waiting for message'));
      cleanup();
    }, timeout);

    function onMessage(raw) {
      const msg = JSON.parse(raw.toString());
      if (predicate(msg)) {
        clearTimeout(timer);
        cleanup();
        resolve(msg);
      }
    }

    function cleanup() {
      ws.off('message', onMessage);
    }

    ws.on('message', onMessage);
  });
}

async function main() {
  console.log(`Connecting to ws://localhost:${PORT}...`);
  const ws = new WebSocket(`ws://localhost:${PORT}`);

  await new Promise((resolve, reject) => {
    ws.on('open', resolve);
    ws.on('error', reject);
  });

  // Step 1: register
  send(ws, {
    type: 'register',
    client: { id: 'smoke-test', name: 'CordCode-Smoke', version: '1.0' },
    protocol: { name: 'cccode-bridge', version: 1 },
  });

  const ack = await waitFor(ws, m => m.type === 'register_ack');
  console.log(`← register_ack: ok=${ack.ok}, backends=${JSON.stringify(ack.backends?.map(b => b.id))}`);
  if (!ack.ok || !ack.backends?.length) {
    throw new Error('register failed: no backends');
  }

  const backendId = ack.backends[0].id;
  console.log(`Using backend: ${backendId}`);

  // Step 2: list_models
  send(ws, {
    type: 'request',
    requestId: 'r1',
    backendId,
    method: 'list_models',
  });

  const models = await waitFor(ws, m => m.type === 'result' && m.requestId === 'r1');
  console.log(`← list_models: ${JSON.stringify(models.data?.models?.map(m => m.id))}`);

  // Step 3: create_session
  send(ws, {
    type: 'request',
    requestId: 'r2',
    backendId,
    method: 'create_session',
    params: { title: 'Smoke Test' },
  });

  const session = await waitFor(ws, m => m.type === 'result' && m.requestId === 'r2', 30000);
  const sessionId = session.data?.sessionId;
  console.log(`← create_session: sessionId=${sessionId}`);
  if (!sessionId) {
    throw new Error('create_session returned no sessionId');
  }

  // Step 4: send_message and collect events
  console.log('\nSending "hello"...');
  send(ws, {
    type: 'request',
    requestId: 'r3',
    backendId,
    method: 'send_message',
    params: { sessionId, content: 'Say "hello world" in exactly those words, nothing else.' },
  });

  // Collect events until turn_completed
  const events = [];
  await new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      reject(new Error('timeout waiting for turn_completed'));
    }, 120000);

    ws.on('message', (raw) => {
      const msg = JSON.parse(raw.toString());
      if (msg.type === 'event' && msg.sessionId === sessionId) {
        events.push(msg);
        if (msg.event === 'text_delta') {
          process.stdout.write(msg.data?.delta || '');
        }
        if (msg.event === 'turn_completed') {
          clearTimeout(timer);
          resolve();
        }
        if (msg.event === 'error') {
          clearTimeout(timer);
          reject(new Error(`agent error: ${msg.data?.message}`));
        }
      }
    });
  });

  console.log(`\n← turn_completed. Total events: ${events.length}`);
  const textDeltas = events.filter(e => e.event === 'text_delta');
  const fullText = textDeltas.map(e => e.data?.delta || '').join('');
  console.log(`Full text: "${fullText}"`);

  // Step 5: get_session_messages
  send(ws, {
    type: 'request',
    requestId: 'r4',
    backendId,
    method: 'get_session_messages',
    params: { sessionId },
  });

  const history = await waitFor(ws, m => m.type === 'result' && m.requestId === 'r4', 15000);
  console.log(`← get_session_messages: ${history.data?.messages?.length} messages`);

  console.log('\n✅ All checks passed!');
  ws.close();
  process.exit(0);
}

main().catch(err => {
  console.error('❌ Smoke test failed:', err.message);
  process.exit(1);
});
