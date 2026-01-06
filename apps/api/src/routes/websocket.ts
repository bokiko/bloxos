import { FastifyInstance } from 'fastify';
import { prisma } from '@bloxos/database';
import { authService } from '../services/auth-service.ts';

interface WebSocketClient {
  socket: WebSocket;
  userId: string;
  subscriptions: Set<string>; // 'rigs', 'alerts', 'stats'
}

const clients: Map<string, WebSocketClient> = new Map();

// Broadcast to all authenticated clients
export function broadcastToAll(event: string, data: unknown) {
  const message = JSON.stringify({ event, data });
  clients.forEach((client) => {
    if (client.socket.readyState === 1) { // OPEN
      client.socket.send(message);
    }
  });
}

// Broadcast to clients subscribed to a specific channel
export function broadcastToSubscribers(channel: string, event: string, data: unknown) {
  const message = JSON.stringify({ event, data });
  clients.forEach((client) => {
    if (client.subscriptions.has(channel) && client.socket.readyState === 1) {
      client.socket.send(message);
    }
  });
}

export async function websocketRoutes(app: FastifyInstance) {
  app.get('/ws', { websocket: true }, (socket, request) => {
    let clientId: string | null = null;
    let userId: string | null = null;

    socket.on('message', async (rawMessage: Buffer) => {
      try {
        const message = JSON.parse(rawMessage.toString());

        // Handle authentication
        if (message.type === 'auth') {
          const token = message.token;
          const payload = authService.verifyToken(token);
          
          if (!payload) {
            socket.send(JSON.stringify({ type: 'error', message: 'Invalid token' }));
            socket.close();
            return;
          }

          userId = payload.userId;
          clientId = `${userId}-${Date.now()}`;
          
          clients.set(clientId, {
            socket: socket as unknown as WebSocket,
            userId,
            subscriptions: new Set(['rigs', 'alerts', 'stats']), // Subscribe to all by default
          });

          socket.send(JSON.stringify({ type: 'authenticated', clientId }));
          
          // Send initial data
          await sendInitialData(socket);
        }

        // Handle subscription changes
        if (message.type === 'subscribe' && clientId) {
          const client = clients.get(clientId);
          if (client && message.channel) {
            client.subscriptions.add(message.channel);
            socket.send(JSON.stringify({ type: 'subscribed', channel: message.channel }));
          }
        }

        if (message.type === 'unsubscribe' && clientId) {
          const client = clients.get(clientId);
          if (client && message.channel) {
            client.subscriptions.delete(message.channel);
            socket.send(JSON.stringify({ type: 'unsubscribed', channel: message.channel }));
          }
        }

        // Handle ping
        if (message.type === 'ping') {
          socket.send(JSON.stringify({ type: 'pong' }));
        }

      } catch (error) {
        console.error('WebSocket message error:', error);
      }
    });

    socket.on('close', () => {
      if (clientId) {
        clients.delete(clientId);
      }
    });

    socket.on('error', (error: Error) => {
      console.error('WebSocket error:', error);
      if (clientId) {
        clients.delete(clientId);
      }
    });
  });
}

async function sendInitialData(socket: unknown) {
  const ws = socket as WebSocket;
  
  try {
    // Send current rig stats
    const rigs = await prisma.rig.findMany({
      include: {
        gpus: true,
        cpu: true,
      },
    });

    ws.send(JSON.stringify({
      type: 'initial',
      event: 'rigs',
      data: rigs,
    }));

    // Send summary stats
    const stats = await getSystemStats();
    ws.send(JSON.stringify({
      type: 'initial',
      event: 'stats',
      data: stats,
    }));

  } catch (error) {
    console.error('Error sending initial data:', error);
  }
}

async function getSystemStats() {
  const rigs = await prisma.rig.findMany({
    include: { gpus: true },
  });

  const totalRigs = rigs.length;
  const onlineRigs = rigs.filter(r => r.status === 'ONLINE').length;
  const warningRigs = rigs.filter(r => r.status === 'WARNING').length;
  
  let totalHashrate = 0;
  let totalPower = 0;
  let totalGpus = 0;

  rigs.forEach(rig => {
    rig.gpus.forEach(gpu => {
      totalGpus++;
      totalHashrate += gpu.hashrate || 0;
      totalPower += gpu.powerDraw || 0;
    });
  });

  const offlineRigs = rigs.filter(r => r.status === 'OFFLINE').length;
  const errorRigs = rigs.filter(r => r.status === 'ERROR').length;

  return {
    totalRigs,
    onlineRigs,
    warningRigs,
    offlineRigs,
    errorRigs,
    totalGpus,
    totalHashrate,
    totalPower,
    efficiency: totalPower > 0 ? (totalHashrate / totalPower).toFixed(2) : 0,
  };
}

// Export for use in gpu-poller to broadcast updates
export { clients, getSystemStats };
