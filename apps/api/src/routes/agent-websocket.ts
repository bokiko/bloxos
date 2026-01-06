import { FastifyInstance, FastifyRequest } from 'fastify';
import { prisma } from '@bloxos/database';
import { validateAgentToken } from '../middleware/authorization.ts';
import { auditLog, sanitizeOutput } from '../utils/security.ts';
import { broadcastToSubscribers } from './websocket.ts';

// ============================================
// TYPES
// ============================================

interface AgentConnection {
  socket: WebSocket;
  rigId: string;
  rigName: string;
  farmId: string;
  connectedAt: Date;
  lastHeartbeat: Date;
}

interface AgentMessage {
  type: 'auth' | 'stats' | 'heartbeat' | 'command_result' | 'miner_status';
  token?: string;
  data?: unknown;
  commandId?: string;
  success?: boolean;
  error?: string;
}

interface Command {
  id: string;
  type: 'start_miner' | 'stop_miner' | 'restart_miner' | 'apply_oc' | 'apply_flight_sheet' | 'reboot' | 'shutdown' | 'execute';
  payload?: unknown;
  createdAt: Date;
}

// ============================================
// STATE
// ============================================

// Connected agents indexed by rigId
const agents: Map<string, AgentConnection> = new Map();

// Pending commands indexed by rigId -> commands queue
const pendingCommands: Map<string, Command[]> = new Map();

// Auth timeout (10 seconds)
const AUTH_TIMEOUT_MS = 10000;

// Heartbeat timeout (60 seconds - mark as offline if no heartbeat)
const HEARTBEAT_TIMEOUT_MS = 60000;

// ============================================
// EXPORTED FUNCTIONS
// ============================================

/**
 * Check if a rig is connected via WebSocket
 */
export function isAgentConnected(rigId: string): boolean {
  return agents.has(rigId);
}

/**
 * Get all connected agents
 */
export function getConnectedAgents(): { rigId: string; rigName: string; connectedAt: Date }[] {
  return Array.from(agents.values()).map(a => ({
    rigId: a.rigId,
    rigName: a.rigName,
    connectedAt: a.connectedAt,
  }));
}

/**
 * Send a command to a specific rig
 * Returns command ID for tracking, or null if rig not connected
 */
export function sendCommandToRig(rigId: string, command: Omit<Command, 'id' | 'createdAt'>): string | null {
  const agent = agents.get(rigId);
  
  const cmd: Command = {
    ...command,
    id: `cmd_${Date.now()}_${Math.random().toString(36).substring(2, 9)}`,
    createdAt: new Date(),
  };

  if (agent && agent.socket.readyState === 1) {
    // Agent is connected, send immediately
    agent.socket.send(JSON.stringify({
      type: 'command',
      command: cmd,
    }));
    
    auditLog({
      action: 'command_sent',
      resource: 'rig',
      resourceId: rigId,
      details: { commandId: cmd.id, commandType: cmd.type },
      success: true,
    });
    
    return cmd.id;
  }

  // Agent not connected, queue the command
  const queue = pendingCommands.get(rigId) || [];
  queue.push(cmd);
  pendingCommands.set(rigId, queue);
  
  auditLog({
    action: 'command_queued',
    resource: 'rig',
    resourceId: rigId,
    details: { commandId: cmd.id, commandType: cmd.type },
    success: true,
  });
  
  return cmd.id;
}

/**
 * Broadcast stats update to dashboard clients
 */
function broadcastRigUpdate(rigId: string, stats: unknown) {
  broadcastToSubscribers('rigs', 'rig_update', {
    rigId,
    stats: sanitizeOutput(stats),
    timestamp: new Date().toISOString(),
  });
}

// ============================================
// WEBSOCKET ROUTE
// ============================================

export async function agentWebsocketRoutes(app: FastifyInstance) {
  // Agent WebSocket endpoint
  app.get('/ws', { websocket: true }, (socket, request: FastifyRequest) => {
    let rigId: string | null = null;
    let authenticated = false;

    // Set authentication timeout
    const authTimeout = setTimeout(() => {
      if (!authenticated) {
        socket.send(JSON.stringify({ type: 'error', message: 'Authentication timeout' }));
        socket.close(4001, 'Authentication timeout');
      }
    }, AUTH_TIMEOUT_MS);

    // Try to authenticate via query parameter token
    const queryToken = (request.query as { token?: string })?.token;
    if (queryToken) {
      authenticateAgent(queryToken);
    }

    // Authentication function
    async function authenticateAgent(token: string) {
      try {
        const validation = await validateAgentToken(token);
        
        if (!validation.valid || !validation.rig) {
          socket.send(JSON.stringify({ type: 'error', message: 'Invalid token' }));
          socket.close(4002, 'Invalid token');
          return;
        }

        clearTimeout(authTimeout);
        authenticated = true;
        rigId = validation.rig.id;

        // Get full rig info
        const rig = await prisma.rig.findUnique({
          where: { id: rigId },
          select: { id: true, name: true, farmId: true },
        });

        if (!rig) {
          socket.send(JSON.stringify({ type: 'error', message: 'Rig not found' }));
          socket.close(4003, 'Rig not found');
          return;
        }

        // Close existing connection if any (prevent duplicates)
        const existingAgent = agents.get(rigId);
        if (existingAgent) {
          existingAgent.socket.close(4004, 'New connection established');
        }

        // Register the agent
        agents.set(rigId, {
          socket: socket as unknown as WebSocket,
          rigId: rig.id,
          rigName: rig.name,
          farmId: rig.farmId,
          connectedAt: new Date(),
          lastHeartbeat: new Date(),
        });

        // Update rig status in database
        await prisma.rig.update({
          where: { id: rigId },
          data: {
            status: 'ONLINE',
            lastSeen: new Date(),
          },
        });

        auditLog({
          action: 'agent_websocket_connect',
          resource: 'rig',
          resourceId: rigId,
          ip: request.ip,
          success: true,
        });

        // Send authentication success
        socket.send(JSON.stringify({
          type: 'authenticated',
          rigId: rig.id,
          rigName: rig.name,
        }));

        // Send any pending commands
        const pending = pendingCommands.get(rigId);
        if (pending && pending.length > 0) {
          for (const cmd of pending) {
            socket.send(JSON.stringify({
              type: 'command',
              command: cmd,
            }));
          }
          pendingCommands.delete(rigId);
        }

        // Notify dashboard clients
        broadcastRigUpdate(rigId, { status: 'ONLINE', connectedAt: new Date() });

      } catch (error) {
        console.error('Agent auth error:', error);
        socket.send(JSON.stringify({ type: 'error', message: 'Authentication failed' }));
        socket.close(4005, 'Authentication failed');
      }
    }

    // Message handler
    socket.on('message', async (rawMessage: Buffer) => {
      try {
        const message: AgentMessage = JSON.parse(rawMessage.toString());

        // Handle authentication
        if (message.type === 'auth' && !authenticated) {
          if (message.token) {
            await authenticateAgent(message.token);
          } else {
            socket.send(JSON.stringify({ type: 'error', message: 'Token required' }));
          }
          return;
        }

        // All other messages require authentication
        if (!authenticated || !rigId) {
          socket.send(JSON.stringify({ type: 'error', message: 'Not authenticated' }));
          return;
        }

        // Update last heartbeat
        const agent = agents.get(rigId);
        if (agent) {
          agent.lastHeartbeat = new Date();
        }

        // Handle different message types
        switch (message.type) {
          case 'heartbeat':
            await handleHeartbeat(rigId);
            socket.send(JSON.stringify({ type: 'heartbeat_ack', timestamp: Date.now() }));
            break;

          case 'stats':
            await handleStats(rigId, message.data);
            break;

          case 'command_result':
            await handleCommandResult(rigId, message);
            break;

          case 'miner_status':
            await handleMinerStatus(rigId, message.data);
            break;

          default:
            socket.send(JSON.stringify({ type: 'error', message: 'Unknown message type' }));
        }

      } catch (error) {
        console.error('Agent message error:', error);
        socket.send(JSON.stringify({ type: 'error', message: 'Invalid message format' }));
      }
    });

    // Connection closed
    socket.on('close', async () => {
      clearTimeout(authTimeout);
      
      if (rigId) {
        agents.delete(rigId);
        
        // Update rig status to offline
        try {
          await prisma.rig.update({
            where: { id: rigId },
            data: { status: 'OFFLINE' },
          });
        } catch (error) {
          console.error('Failed to update rig status:', error);
        }

        auditLog({
          action: 'agent_websocket_disconnect',
          resource: 'rig',
          resourceId: rigId,
          success: true,
        });

        // Notify dashboard clients
        broadcastRigUpdate(rigId, { status: 'OFFLINE' });
      }
    });

    // Connection error
    socket.on('error', (error: Error) => {
      console.error('Agent WebSocket error:', error);
      clearTimeout(authTimeout);
      
      if (rigId) {
        agents.delete(rigId);
      }
    });
  });
}

// ============================================
// MESSAGE HANDLERS
// ============================================

async function handleHeartbeat(rigId: string) {
  await prisma.rig.update({
    where: { id: rigId },
    data: {
      status: 'ONLINE',
      lastSeen: new Date(),
    },
  });
}

async function handleStats(rigId: string, data: unknown) {
  if (!data || typeof data !== 'object') return;

  const stats = data as {
    gpus?: Array<{
      index: number;
      name?: string;
      temperature?: number | null;
      memTemp?: number | null;
      fanSpeed?: number | null;
      powerDraw?: number | null;
      coreClock?: number | null;
      memoryClock?: number | null;
      utilization?: number | null;
      hashrate?: number | null;
      vram?: number;
      busId?: string;
    }>;
    cpu?: {
      model?: string;
      vendor?: string;
      cores?: number;
      threads?: number;
      temperature?: number | null;
      usage?: number | null;
      frequency?: number | null;
      powerDraw?: number | null;
    };
  };

  // Update rig status
  await prisma.rig.update({
    where: { id: rigId },
    data: {
      status: 'ONLINE',
      lastSeen: new Date(),
    },
  });

  // Update GPU stats
  if (stats.gpus && stats.gpus.length > 0) {
    for (const gpu of stats.gpus) {
      const existingGpu = await prisma.gPU.findFirst({
        where: { rigId, index: gpu.index },
      });

      if (existingGpu) {
        await prisma.gPU.update({
          where: { id: existingGpu.id },
          data: {
            name: gpu.name || existingGpu.name,
            temperature: gpu.temperature ?? existingGpu.temperature,
            memTemp: gpu.memTemp ?? existingGpu.memTemp,
            fanSpeed: gpu.fanSpeed ?? existingGpu.fanSpeed,
            powerDraw: gpu.powerDraw ?? existingGpu.powerDraw,
            coreClock: gpu.coreClock ?? existingGpu.coreClock,
            memoryClock: gpu.memoryClock ?? existingGpu.memoryClock,
            hashrate: gpu.hashrate ?? existingGpu.hashrate,
            vram: gpu.vram || existingGpu.vram,
            busId: gpu.busId || existingGpu.busId,
          },
        });
      } else if (gpu.name) {
        await prisma.gPU.create({
          data: {
            rigId,
            index: gpu.index,
            name: gpu.name,
            vendor: gpu.name.toLowerCase().includes('nvidia') ? 'NVIDIA' :
                    gpu.name.toLowerCase().includes('amd') ? 'AMD' : 'INTEL',
            vram: gpu.vram || 0,
            busId: gpu.busId,
            temperature: gpu.temperature,
            memTemp: gpu.memTemp,
            fanSpeed: gpu.fanSpeed,
            powerDraw: gpu.powerDraw,
            coreClock: gpu.coreClock,
            memoryClock: gpu.memoryClock,
            hashrate: gpu.hashrate,
          },
        });
      }
    }
  }

  // Update CPU stats
  if (stats.cpu) {
    const cpu = stats.cpu;
    const existingCpu = await prisma.cPU.findUnique({
      where: { rigId },
    });

    if (existingCpu) {
      await prisma.cPU.update({
        where: { id: existingCpu.id },
        data: {
          model: cpu.model || existingCpu.model,
          vendor: cpu.vendor || existingCpu.vendor,
          cores: cpu.cores || existingCpu.cores,
          threads: cpu.threads || existingCpu.threads,
          temperature: cpu.temperature ?? existingCpu.temperature,
          usage: cpu.usage ?? existingCpu.usage,
          frequency: cpu.frequency ?? existingCpu.frequency,
          powerDraw: cpu.powerDraw ?? existingCpu.powerDraw,
        },
      });
    } else if (cpu.model) {
      await prisma.cPU.create({
        data: {
          rigId,
          model: cpu.model,
          vendor: cpu.vendor || 'Unknown',
          cores: cpu.cores || 1,
          threads: cpu.threads || 1,
          temperature: cpu.temperature,
          usage: cpu.usage,
          frequency: cpu.frequency,
          powerDraw: cpu.powerDraw,
        },
      });
    }
  }

  // Broadcast update to dashboard
  broadcastRigUpdate(rigId, stats);
}

async function handleCommandResult(rigId: string, message: AgentMessage) {
  auditLog({
    action: 'command_result',
    resource: 'rig',
    resourceId: rigId,
    details: {
      commandId: message.commandId,
      success: message.success,
      error: message.error,
    },
    success: message.success ?? false,
  });

  // Broadcast to dashboard
  broadcastToSubscribers('rigs', 'command_result', {
    rigId,
    commandId: message.commandId,
    success: message.success,
    error: message.error,
    timestamp: new Date().toISOString(),
  });
}

async function handleMinerStatus(rigId: string, data: unknown) {
  if (!data || typeof data !== 'object') return;

  const status = data as {
    name?: string;
    version?: string;
    running?: boolean;
    algo?: string;
    pool?: string;
    wallet?: string;
    hashrate?: number;
    shares?: { accepted?: number; rejected?: number };
    pid?: number;
  };

  // Update or create MinerInstance
  const existingMiner = await prisma.minerInstance.findFirst({
    where: { rigId },
    orderBy: { updatedAt: 'desc' },
  });

  if (existingMiner) {
    await prisma.minerInstance.update({
      where: { id: existingMiner.id },
      data: {
        minerName: status.name || existingMiner.minerName,
        algo: status.algo || existingMiner.algo,
        pool: status.pool || existingMiner.pool,
        wallet: status.wallet || existingMiner.wallet,
        status: status.running ? 'RUNNING' : 'STOPPED',
        hashrate: status.hashrate ?? existingMiner.hashrate,
        accepted: status.shares?.accepted ?? existingMiner.accepted,
        rejected: status.shares?.rejected ?? existingMiner.rejected,
        pid: status.pid ?? existingMiner.pid,
      },
    });
  } else if (status.name) {
    await prisma.minerInstance.create({
      data: {
        rigId,
        minerName: status.name,
        algo: status.algo || 'unknown',
        pool: status.pool || '',
        wallet: status.wallet || '',
        status: status.running ? 'RUNNING' : 'STOPPED',
        hashrate: status.hashrate,
        accepted: status.shares?.accepted || 0,
        rejected: status.shares?.rejected || 0,
        pid: status.pid,
        startedAt: status.running ? new Date() : null,
      },
    });
  }

  // Broadcast to dashboard
  broadcastRigUpdate(rigId, { miner: status });
}

// ============================================
// HEARTBEAT MONITOR
// ============================================

// Check for stale connections every 30 seconds
setInterval(async () => {
  const now = Date.now();
  
  for (const [rigId, agent] of agents) {
    const timeSinceHeartbeat = now - agent.lastHeartbeat.getTime();
    
    if (timeSinceHeartbeat > HEARTBEAT_TIMEOUT_MS) {
      console.log(`Agent ${rigId} heartbeat timeout, closing connection`);
      agent.socket.close(4006, 'Heartbeat timeout');
      agents.delete(rigId);
      
      // Update rig status
      try {
        await prisma.rig.update({
          where: { id: rigId },
          data: { status: 'OFFLINE' },
        });
      } catch (error) {
        console.error('Failed to update rig status:', error);
      }
      
      broadcastRigUpdate(rigId, { status: 'OFFLINE' });
    }
  }
}, 30000);
