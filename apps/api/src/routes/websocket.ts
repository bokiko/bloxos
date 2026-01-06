import { FastifyInstance, FastifyRequest } from 'fastify';
import { prisma } from '@bloxos/database';
import { authService } from '../services/auth-service.ts';
import { sanitizeOutput, createSafeWSMessage, validateWSMessage, auditLog } from '../utils/security.ts';
import { getUserRigFilter } from '../middleware/authorization.ts';

interface WebSocketClient {
  socket: WebSocket;
  userId: string;
  role: string;
  subscriptions: Set<string>; // 'rigs', 'alerts', 'stats'
}

const clients: Map<string, WebSocketClient> = new Map();

// Authentication timeout (10 seconds to authenticate after connection)
const AUTH_TIMEOUT_MS = 10000;

// Broadcast to all authenticated clients
export function broadcastToAll(event: string, data: unknown) {
  const message = createSafeWSMessage('broadcast', { event, data: sanitizeOutput(data) });
  clients.forEach((client) => {
    if (client.socket.readyState === 1) { // OPEN
      client.socket.send(message);
    }
  });
}

// Broadcast to clients subscribed to a specific channel
export function broadcastToSubscribers(channel: string, event: string, data: unknown) {
  const message = createSafeWSMessage('broadcast', { event, channel, data: sanitizeOutput(data) });
  clients.forEach((client) => {
    if (client.subscriptions.has(channel) && client.socket.readyState === 1) {
      client.socket.send(message);
    }
  });
}

export async function websocketRoutes(app: FastifyInstance) {
  // WebSocket endpoint with query parameter authentication option
  app.get('/ws', { websocket: true }, (socket, request: FastifyRequest) => {
    let clientId: string | null = null;
    let userId: string | null = null;
    let userRole: string | null = null;
    let authenticated = false;
    
    // Set authentication timeout - close connection if not authenticated in time
    const authTimeout = setTimeout(() => {
      if (!authenticated) {
        socket.send(createSafeWSMessage('error', { message: 'Authentication timeout' }));
        socket.close(4001, 'Authentication timeout');
      }
    }, AUTH_TIMEOUT_MS);

    // Try to authenticate via query parameter token first
    const queryToken = (request.query as { token?: string })?.token;
    if (queryToken) {
      authenticateClient(queryToken);
    }

    async function authenticateClient(token: string) {
      const payload = await authService.verifyToken(token);
      
      if (!payload) {
        socket.send(createSafeWSMessage('error', { message: 'Invalid token' }));
        socket.close(4002, 'Invalid token');
        return false;
      }

      clearTimeout(authTimeout);
      authenticated = true;
      userId = payload.userId;
      userRole = payload.role;
      clientId = `${userId}-${Date.now()}`;
      
      clients.set(clientId, {
        socket: socket as unknown as WebSocket,
        userId,
        role: userRole,
        subscriptions: new Set(['rigs', 'alerts', 'stats']), // Subscribe to all by default
      });

      auditLog({
        userId,
        action: 'websocket_connect',
        resource: 'websocket',
        success: true,
        ip: request.ip,
      });

      socket.send(createSafeWSMessage('authenticated', { clientId }));
      
      // Send initial data (filtered by user permissions)
      await sendInitialData(socket, { userId, role: userRole });
      
      return true;
    }

    socket.on('message', async (rawMessage: Buffer) => {
      try {
        const validation = validateWSMessage(rawMessage.toString());
        
        if (!validation.valid || !validation.message) {
          socket.send(createSafeWSMessage('error', { message: validation.error }));
          return;
        }

        const message = validation.message;

        // Handle authentication
        if (message.type === 'auth' && !authenticated) {
          const token = message.token as string;
          if (!token || typeof token !== 'string') {
            socket.send(createSafeWSMessage('error', { message: 'Token required' }));
            return;
          }
          await authenticateClient(token);
          return;
        }

        // All other messages require authentication
        if (!authenticated) {
          socket.send(createSafeWSMessage('error', { message: 'Not authenticated' }));
          return;
        }

        // Handle subscription changes
        if (message.type === 'subscribe' && clientId) {
          const client = clients.get(clientId);
          const channel = message.channel as string;
          if (client && channel && typeof channel === 'string') {
            // Validate channel name
            if (['rigs', 'alerts', 'stats'].includes(channel)) {
              client.subscriptions.add(channel);
              socket.send(createSafeWSMessage('subscribed', { channel }));
            } else {
              socket.send(createSafeWSMessage('error', { message: 'Invalid channel' }));
            }
          }
        }

        if (message.type === 'unsubscribe' && clientId) {
          const client = clients.get(clientId);
          const channel = message.channel as string;
          if (client && channel && typeof channel === 'string') {
            client.subscriptions.delete(channel);
            socket.send(createSafeWSMessage('unsubscribed', { channel }));
          }
        }

        // Handle ping
        if (message.type === 'ping') {
          socket.send(createSafeWSMessage('pong', { timestamp: Date.now() }));
        }

      } catch (error) {
        console.error('WebSocket message error:', error);
        socket.send(createSafeWSMessage('error', { message: 'Internal error' }));
      }
    });

    socket.on('close', () => {
      clearTimeout(authTimeout);
      if (clientId) {
        clients.delete(clientId);
        if (userId) {
          auditLog({
            userId,
            action: 'websocket_disconnect',
            resource: 'websocket',
            success: true,
          });
        }
      }
    });

    socket.on('error', (error: Error) => {
      console.error('WebSocket error:', error);
      clearTimeout(authTimeout);
      if (clientId) {
        clients.delete(clientId);
      }
    });
  });
}

async function sendInitialData(socket: unknown, user: { userId: string; role: string }) {
  const ws = socket as WebSocket;
  
  try {
    // Filter rigs based on user permissions
    const filter = getUserRigFilter(user);
    
    // Send current rig stats (filtered by user access)
    const rigs = await prisma.rig.findMany({
      where: filter,
      include: {
        gpus: true,
        cpu: true,
      },
    });

    // Sanitize output before sending
    ws.send(createSafeWSMessage('initial', {
      event: 'rigs',
      data: sanitizeOutput(rigs),
    }));

    // Send summary stats
    const stats = await getSystemStats(user);
    ws.send(createSafeWSMessage('initial', {
      event: 'stats',
      data: sanitizeOutput(stats),
    }));

  } catch (error) {
    console.error('Error sending initial data:', error);
    ws.send(createSafeWSMessage('error', { message: 'Failed to load initial data' }));
  }
}

async function getSystemStats(user: { userId: string; role: string }) {
  const filter = getUserRigFilter(user);
  
  const rigs = await prisma.rig.findMany({
    where: filter,
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
