# BloxOS API Documentation

> REST API reference for BloxOS

**Base URL:** `http://your-server:3001/api`

---

## Table of Contents

1. [Authentication](#authentication)
2. [Rigs](#rigs)
3. [Wallets](#wallets)
4. [Pools](#pools)
5. [Flight Sheets](#flight-sheets)
6. [OC Profiles](#oc-profiles)
7. [Alerts](#alerts)
8. [Users](#users)
9. [WebSocket](#websocket)

---

## Authentication

All endpoints (except `/auth/*`) require authentication via JWT token.

### Headers

```
Authorization: Bearer <token>
X-CSRF-Token: <csrf-token>
```

### Login

```http
POST /auth/login
Content-Type: application/json

{
  "email": "user@example.com",
  "password": "password123"
}
```

**Response:**
```json
{
  "token": "eyJhbGciOiJIUzI1NiIs...",
  "refreshToken": "eyJhbGciOiJIUzI1NiIs...",
  "user": {
    "id": "uuid",
    "email": "user@example.com",
    "role": "ADMIN"
  }
}
```

### Register

```http
POST /auth/register
Content-Type: application/json

{
  "email": "user@example.com",
  "password": "SecurePass123!",
  "name": "John Doe"
}
```

### Refresh Token

```http
POST /auth/refresh
Content-Type: application/json

{
  "refreshToken": "eyJhbGciOiJIUzI1NiIs..."
}
```

### Logout

```http
POST /auth/logout
```

---

## Rigs

### List Rigs

```http
GET /rigs
```

**Query Parameters:**
- `status` - Filter by status: `ONLINE`, `OFFLINE`, `WARNING`, `ERROR`
- `groupId` - Filter by rig group

**Response:**
```json
[
  {
    "id": "uuid",
    "name": "Rig-01",
    "hostname": "rig01.local",
    "status": "ONLINE",
    "lastSeen": "2026-01-07T12:00:00Z",
    "gpus": [...],
    "cpu": {...},
    "minerInstances": [...]
  }
]
```

### Get Rig

```http
GET /rigs/:id
```

### Create Rig

```http
POST /rigs
Content-Type: application/json

{
  "name": "Rig-01",
  "hostname": "192.168.1.100"
}
```

**Response:**
```json
{
  "id": "uuid",
  "name": "Rig-01",
  "token": "rig-token-for-agent"
}
```

### Update Rig

```http
PUT /rigs/:id
Content-Type: application/json

{
  "name": "Rig-01-Updated",
  "gpuMiningEnabled": true,
  "cpuMiningEnabled": false
}
```

### Delete Rig

```http
DELETE /rigs/:id
```

### Rig Commands

```http
POST /rigs/:id/command
Content-Type: application/json

{
  "type": "start_miner" | "stop_miner" | "restart_miner" | "reboot" | "shutdown",
  "payload": {}
}
```

### Apply Flight Sheet

```http
POST /rigs/:id/flight-sheet
Content-Type: application/json

{
  "flightSheetId": "uuid"
}
```

### Apply OC Profile

```http
POST /rigs/:id/oc-profile
Content-Type: application/json

{
  "ocProfileId": "uuid"
}
```

---

## Wallets

### List Wallets

```http
GET /wallets
```

### Create Wallet

```http
POST /wallets
Content-Type: application/json

{
  "name": "ETH Main",
  "coin": "ETH",
  "address": "0x742d35Cc6634C0532925a3b844Bc9e7595f..."
}
```

### Update Wallet

```http
PUT /wallets/:id
Content-Type: application/json

{
  "name": "ETH Main Updated"
}
```

### Delete Wallet

```http
DELETE /wallets/:id
```

---

## Pools

### List Pools

```http
GET /pools
```

### Create Pool

```http
POST /pools
Content-Type: application/json

{
  "name": "Ethermine",
  "coin": "ETH",
  "url": "stratum+tcp://eth.2miners.com:2020",
  "urlSSL": "stratum+ssl://eth.2miners.com:12020"
}
```

### Update Pool

```http
PUT /pools/:id
```

### Delete Pool

```http
DELETE /pools/:id
```

---

## Flight Sheets

### List Flight Sheets

```http
GET /flight-sheets
```

### Get Flight Sheet

```http
GET /flight-sheets/:id
```

### Create Flight Sheet

```http
POST /flight-sheets
Content-Type: application/json

{
  "name": "ETH Mining",
  "walletId": "uuid",
  "poolId": "uuid",
  "miner": "t-rex",
  "algo": "ethash",
  "extraArgs": "--api-bind-http 0.0.0.0:4067"
}
```

### Update Flight Sheet

```http
PUT /flight-sheets/:id
```

### Delete Flight Sheet

```http
DELETE /flight-sheets/:id
```

---

## OC Profiles

### List OC Profiles

```http
GET /oc-profiles
```

### Create OC Profile

```http
POST /oc-profiles
Content-Type: application/json

{
  "name": "ETH Optimal",
  "vendor": "NVIDIA",
  "powerLimit": 120,
  "coreOffset": -200,
  "memOffset": 1200,
  "fanSpeed": 70
}
```

### Update OC Profile

```http
PUT /oc-profiles/:id
```

### Delete OC Profile

```http
DELETE /oc-profiles/:id
```

---

## Alerts

### List Alerts

```http
GET /alerts
```

**Query Parameters:**
- `status` - Filter: `ACTIVE`, `RESOLVED`, `ACKNOWLEDGED`
- `rigId` - Filter by rig

### Get Alert Config

```http
GET /alerts/config/:rigId
```

### Update Alert Config

```http
PUT /alerts/config/:rigId
Content-Type: application/json

{
  "gpuTempThreshold": 80,
  "cpuTempThreshold": 85,
  "offlineTimeout": 300,
  "hashrateDropPercent": 20,
  "enabled": true
}
```

### Acknowledge Alert

```http
POST /alerts/:id/acknowledge
```

### Resolve Alert

```http
POST /alerts/:id/resolve
```

---

## Users

### List Users (Admin only)

```http
GET /users
```

### Get Current User

```http
GET /users/me
```

### Update User

```http
PUT /users/:id
Content-Type: application/json

{
  "name": "John Updated",
  "role": "USER"
}
```

### Change Password

```http
POST /users/change-password
Content-Type: application/json

{
  "currentPassword": "oldPass123",
  "newPassword": "newSecurePass123!"
}
```

### Delete User (Admin only)

```http
DELETE /users/:id
```

---

## Rig Groups

### List Rig Groups

```http
GET /rig-groups
```

### Create Rig Group

```http
POST /rig-groups
Content-Type: application/json

{
  "name": "GPU Farm 1",
  "color": "#3B82F6"
}
```

### Add Rig to Group

```http
POST /rig-groups/:id/rigs
Content-Type: application/json

{
  "rigIds": ["uuid1", "uuid2"]
}
```

### Remove Rig from Group

```http
DELETE /rig-groups/:id/rigs/:rigId
```

---

## Bulk Actions

### Bulk Command

```http
POST /bulk/command
Content-Type: application/json

{
  "rigIds": ["uuid1", "uuid2"],
  "command": {
    "type": "restart_miner"
  }
}
```

### Bulk Apply Flight Sheet

```http
POST /bulk/flight-sheet
Content-Type: application/json

{
  "rigIds": ["uuid1", "uuid2"],
  "flightSheetId": "uuid"
}
```

### Bulk Apply OC Profile

```http
POST /bulk/oc-profile
Content-Type: application/json

{
  "rigIds": ["uuid1", "uuid2"],
  "ocProfileId": "uuid"
}
```

---

## WebSocket

Connect to `ws://your-server:3001/api/ws` for real-time updates.

### Authentication

After connecting, send auth message:

```json
{
  "type": "auth",
  "token": "your-jwt-token"
}
```

### Subscribe to Channels

```json
{
  "type": "subscribe",
  "channel": "rigs" | "alerts" | "stats"
}
```

### Message Types

**Rig Update:**
```json
{
  "type": "broadcast",
  "event": "rig-update",
  "data": {
    "rigId": "uuid",
    "stats": {...}
  }
}
```

**Alert:**
```json
{
  "type": "broadcast",
  "event": "alert",
  "data": {
    "id": "uuid",
    "type": "GPU_TEMP",
    "message": "GPU 0 temperature high: 85°C"
  }
}
```

**Stats Update:**
```json
{
  "type": "broadcast",
  "event": "stats-update",
  "data": {
    "totalRigs": 10,
    "onlineRigs": 8,
    "totalHashrate": 1250.5,
    "totalPower": 2400
  }
}
```

---

## Agent WebSocket

Agents connect to `ws://your-server:3001/api/agent/ws`.

### Authentication

Query param or message:

```
ws://server:3001/api/agent/ws?token=RIG_TOKEN
```

Or send:
```json
{
  "type": "auth",
  "token": "RIG_TOKEN"
}
```

### Sending Stats

```json
{
  "type": "stats",
  "data": {
    "gpus": [
      {
        "index": 0,
        "name": "RTX 3080",
        "temperature": 65,
        "fanSpeed": 70,
        "powerDraw": 220,
        "hashrate": 98.5
      }
    ],
    "cpu": {
      "model": "AMD Ryzen 5 5600X",
      "temperature": 55,
      "usage": 15
    }
  }
}
```

### Receiving Commands

```json
{
  "type": "command",
  "command": {
    "id": "cmd_123",
    "type": "restart_miner",
    "payload": {}
  }
}
```

### Sending Command Result

```json
{
  "type": "command_result",
  "commandId": "cmd_123",
  "success": true,
  "error": null
}
```

---

## Error Responses

All errors follow this format:

```json
{
  "error": "Error message",
  "statusCode": 400,
  "requestId": "req_abc123"
}
```

### Common Status Codes

| Code | Description |
|------|-------------|
| 400 | Bad Request - Invalid input |
| 401 | Unauthorized - Missing or invalid token |
| 403 | Forbidden - Insufficient permissions |
| 404 | Not Found - Resource doesn't exist |
| 429 | Too Many Requests - Rate limited |
| 500 | Internal Server Error |

---

## Rate Limits

- Default: 100 requests per minute per IP
- Auth endpoints: 10 requests per minute per IP
- WebSocket: No rate limit after auth

---

*Last Updated: January 2026*
