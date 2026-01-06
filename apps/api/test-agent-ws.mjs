#!/usr/bin/env node
/**
 * Test script for Agent WebSocket endpoint
 * Usage: node test-agent-ws.mjs [token]
 */

import WebSocket from 'ws';

const SERVER_URL = process.env.SERVER_URL || 'ws://localhost:3001';
const TOKEN = process.argv[2] || 'test-token-12345';

console.log('='.repeat(50));
console.log('Agent WebSocket Test');
console.log('='.repeat(50));
console.log(`Server: ${SERVER_URL}`);
console.log(`Token: ${TOKEN}`);
console.log('');

// Connect with token in query param
const wsUrl = `${SERVER_URL}/api/agent/ws?token=${TOKEN}`;
console.log(`Connecting to: ${wsUrl}`);

const ws = new WebSocket(wsUrl);

let authenticated = false;
let messageCount = 0;

ws.on('open', () => {
  console.log('[CONNECTED] WebSocket connection established');
});

ws.on('message', (data) => {
  messageCount++;
  const msg = JSON.parse(data.toString());
  console.log(`[MESSAGE ${messageCount}] Type: ${msg.type}`);
  console.log(JSON.stringify(msg, null, 2));
  console.log('');

  if (msg.type === 'authenticated') {
    authenticated = true;
    console.log('[SUCCESS] Authenticated successfully!');
    console.log(`  Rig ID: ${msg.rigId}`);
    console.log(`  Rig Name: ${msg.rigName}`);
    console.log('');

    // Send a test heartbeat
    setTimeout(() => {
      console.log('[SENDING] Heartbeat...');
      ws.send(JSON.stringify({ type: 'heartbeat' }));
    }, 1000);

    // Send test stats
    setTimeout(() => {
      console.log('[SENDING] Test stats...');
      ws.send(JSON.stringify({
        type: 'stats',
        data: {
          gpus: [
            {
              index: 0,
              name: 'NVIDIA GeForce RTX 3080',
              temperature: 65,
              fanSpeed: 70,
              powerDraw: 220,
              coreClock: 1800,
              memoryClock: 9500,
              vram: 10240,
              busId: '00:01.0'
            }
          ],
          cpu: {
            model: 'AMD Ryzen 9 5900X',
            vendor: 'AMD',
            cores: 12,
            threads: 24,
            temperature: 55,
            usage: 15.5
          }
        }
      }));
    }, 2000);

    // Close after tests
    setTimeout(() => {
      console.log('[CLOSING] Test complete, closing connection...');
      ws.close();
    }, 4000);

  } else if (msg.type === 'error') {
    console.log(`[ERROR] ${msg.message}`);
    ws.close();
  } else if (msg.type === 'heartbeat_ack') {
    console.log('[SUCCESS] Heartbeat acknowledged');
  }
});

ws.on('close', (code, reason) => {
  console.log(`[CLOSED] Code: ${code}, Reason: ${reason.toString() || 'none'}`);
  console.log('');
  
  if (authenticated) {
    console.log('='.repeat(50));
    console.log('TEST PASSED - WebSocket communication working!');
    console.log('='.repeat(50));
    process.exit(0);
  } else {
    console.log('='.repeat(50));
    console.log('TEST FAILED - Could not authenticate');
    console.log('='.repeat(50));
    process.exit(1);
  }
});

ws.on('error', (err) => {
  console.log(`[ERROR] ${err.message}`);
  process.exit(1);
});

// Timeout after 10 seconds
setTimeout(() => {
  console.log('[TIMEOUT] Test timed out after 10 seconds');
  ws.close();
  process.exit(1);
}, 10000);
