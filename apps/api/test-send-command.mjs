#!/usr/bin/env node
/**
 * Test sending a command to a connected agent
 * This makes an HTTP request to a test endpoint
 */

import http from 'http';

const RIG_ID = process.argv[2] || 'test-rig-001';
const COMMAND = process.argv[3] || 'stop_miner';

// Create a simple POST request to test endpoint
const data = JSON.stringify({
  rigId: RIG_ID,
  command: COMMAND,
});

const options = {
  hostname: 'localhost',
  port: 3001,
  path: '/api/test/send-command',
  method: 'POST',
  headers: {
    'Content-Type': 'application/json',
    'Content-Length': data.length,
  },
};

console.log(`Sending ${COMMAND} command to rig ${RIG_ID}...`);

const req = http.request(options, (res) => {
  let body = '';
  res.on('data', (chunk) => body += chunk);
  res.on('end', () => {
    console.log(`Status: ${res.statusCode}`);
    console.log(`Response: ${body}`);
  });
});

req.on('error', (e) => {
  console.error(`Error: ${e.message}`);
});

req.write(data);
req.end();
