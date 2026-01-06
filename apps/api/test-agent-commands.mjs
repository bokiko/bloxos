#!/usr/bin/env node
/**
 * Test script for Agent WebSocket commands
 * Usage: node test-agent-commands.mjs [token]
 */

import WebSocket from 'ws';

const SERVER_URL = process.env.SERVER_URL || 'ws://localhost:3001';
const TOKEN = process.argv[2] || 'test-agent-token-12345';

console.log('='.repeat(50));
console.log('Agent Command Test');
console.log('='.repeat(50));
console.log(`Server: ${SERVER_URL}`);
console.log(`Token: ${TOKEN}`);
console.log('');

const wsUrl = `${SERVER_URL}/api/agent/ws?token=${TOKEN}`;
console.log(`Connecting to: ${wsUrl}`);

const ws = new WebSocket(wsUrl);

let authenticated = false;
let commandsReceived = 0;

ws.on('open', () => {
  console.log('[CONNECTED] WebSocket connection established');
});

ws.on('message', (data) => {
  const msg = JSON.parse(data.toString());
  console.log(`[MESSAGE] Type: ${msg.type}`);

  if (msg.type === 'authenticated') {
    authenticated = true;
    console.log('[SUCCESS] Authenticated!');
    console.log(`  Rig ID: ${msg.rigId}`);
    console.log('');
    console.log('Waiting for commands... (send via API)');
    console.log('Example: curl -X POST http://localhost:3001/api/rigs/test-rig-001/command/stop-miner');
    console.log('');

  } else if (msg.type === 'command') {
    commandsReceived++;
    console.log('');
    console.log('='.repeat(40));
    console.log(`[COMMAND RECEIVED] #${commandsReceived}`);
    console.log(`  ID: ${msg.command.id}`);
    console.log(`  Type: ${msg.command.type}`);
    console.log(`  Payload: ${JSON.stringify(msg.command.payload || {})}`);
    console.log('='.repeat(40));

    // Simulate command execution
    setTimeout(() => {
      const result = {
        type: 'command_result',
        commandId: msg.command.id,
        success: true,
        error: null,
      };
      console.log(`[SENDING] Command result: success`);
      ws.send(JSON.stringify(result));
    }, 500);

  } else if (msg.type === 'error') {
    console.log(`[ERROR] ${msg.message}`);
    ws.close();
  } else if (msg.type === 'heartbeat_ack') {
    // Silently acknowledge
  } else {
    console.log(JSON.stringify(msg, null, 2));
  }
});

ws.on('close', (code, reason) => {
  console.log(`[CLOSED] Code: ${code}`);
  console.log(`Commands received: ${commandsReceived}`);
  process.exit(0);
});

ws.on('error', (err) => {
  console.log(`[ERROR] ${err.message}`);
  process.exit(1);
});

// Send heartbeat every 30 seconds
setInterval(() => {
  if (ws.readyState === 1) {
    ws.send(JSON.stringify({ type: 'heartbeat' }));
  }
}, 30000);

// Keep alive
console.log('Press Ctrl+C to exit');
