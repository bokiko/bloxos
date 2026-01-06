import { FastifyInstance, FastifyRequest } from 'fastify';
import { Client } from 'ssh2';
import { prisma } from '@bloxos/database';
import { authService } from '../services/auth-service.ts';
import { decrypt } from '../utils/encryption.ts';
import { createSafeWSMessage, validateWSMessage, auditLog } from '../utils/security.ts';
import { getUserRigFilter } from '../middleware/authorization.ts';

interface TerminalParams {
  rigId: string;
}

// Authentication timeout (10 seconds to authenticate after connection)
const AUTH_TIMEOUT_MS = 10000;

// Sanitize terminal output - only escape control characters that could be malicious
// but preserve ANSI escape codes for terminal colors
function sanitizeTerminalOutput(data: string): string {
  // Remove potential script injection while preserving ANSI escape codes
  // ANSI escape codes start with ESC (0x1B) followed by [ and end with a letter
  // We'll preserve those but escape other potentially dangerous sequences
  return data
    // Escape literal HTML tags that might be in output
    .replace(/<script/gi, '&lt;script')
    .replace(/<\/script/gi, '&lt;/script')
    .replace(/<iframe/gi, '&lt;iframe')
    .replace(/<object/gi, '&lt;object')
    .replace(/<embed/gi, '&lt;embed')
    // Remove null bytes which can be used for attacks
    .replace(/\x00/g, '');
}

export async function terminalRoutes(app: FastifyInstance) {
  // WebSocket terminal connection with query parameter authentication option
  app.get<{ Params: TerminalParams; Querystring: { token?: string } }>(
    '/ws/:rigId',
    { websocket: true },
    async (socket, request: FastifyRequest<{ Params: TerminalParams; Querystring: { token?: string } }>) => {
      const { rigId } = request.params;
      let sshClient: Client | null = null;
      let authenticated = false;
      let userId: string | null = null;
      let userRole: string | null = null;

      // Validate rigId format (UUID)
      if (!/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(rigId)) {
        socket.send(createSafeWSMessage('error', { message: 'Invalid rig ID format' }));
        socket.close(4000, 'Invalid rig ID');
        return;
      }

      // Set authentication timeout
      const authTimeout = setTimeout(() => {
        if (!authenticated) {
          socket.send(createSafeWSMessage('error', { message: 'Authentication timeout' }));
          socket.close(4001, 'Authentication timeout');
        }
      }, AUTH_TIMEOUT_MS);

      // Try to authenticate via query parameter token first
      const queryToken = request.query.token;
      if (queryToken) {
        await authenticateAndConnect(queryToken);
      }

      async function authenticateAndConnect(token: string): Promise<boolean> {
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

        // Check if user has access to this rig
        const rigFilter = getUserRigFilter({ userId, role: userRole });
        const rig = await prisma.rig.findFirst({
          where: {
            id: rigId,
            ...rigFilter,
          },
          include: { sshCredential: true },
        });

        if (!rig) {
          socket.send(createSafeWSMessage('error', { message: 'Rig not found or access denied' }));
          socket.close(4003, 'Access denied');
          return false;
        }

        if (!rig.sshCredential) {
          socket.send(createSafeWSMessage('error', { message: 'No SSH credentials configured' }));
          socket.close(4004, 'No SSH credentials');
          return false;
        }

        // Decrypt credentials
        let password: string | undefined;
        let privateKey: string | undefined;

        if (rig.sshCredential.encryptedPassword) {
          password = decrypt(rig.sshCredential.encryptedPassword);
        }
        if (rig.sshCredential.encryptedPrivateKey) {
          privateKey = decrypt(rig.sshCredential.encryptedPrivateKey);
        }

        if (!password && !privateKey) {
          socket.send(createSafeWSMessage('error', { message: 'No authentication method configured' }));
          socket.close(4004, 'No auth method');
          return false;
        }

        auditLog({
          userId,
          action: 'terminal_connect',
          resource: 'rig',
          resourceId: rigId,
          details: { host: rig.sshCredential.host },
          success: true,
          ip: request.ip,
        });

        // Connect via SSH
        sshClient = new Client();

        sshClient.on('ready', () => {
          socket.send(createSafeWSMessage('connected', { rigId, hostname: rig.hostname }));

          sshClient!.shell({ term: 'xterm-256color', cols: 120, rows: 30 }, (err, stream) => {
            if (err) {
              socket.send(createSafeWSMessage('error', { message: 'Failed to open shell' }));
              return;
            }

            // Send SSH output to WebSocket (sanitized)
            stream.on('data', (data: Buffer) => {
              const sanitizedOutput = sanitizeTerminalOutput(data.toString('utf8'));
              socket.send(createSafeWSMessage('output', { data: sanitizedOutput }));
            });

            stream.stderr.on('data', (data: Buffer) => {
              const sanitizedOutput = sanitizeTerminalOutput(data.toString('utf8'));
              socket.send(createSafeWSMessage('output', { data: sanitizedOutput }));
            });

            stream.on('close', () => {
              socket.send(createSafeWSMessage('disconnected', { reason: 'Session closed' }));
              socket.close();
            });

            // Store stream reference for input handling
            (socket as unknown as { sshStream: typeof stream }).sshStream = stream;
          });
        });

        sshClient.on('error', (err: Error) => {
        auditLog({
          userId: userId || undefined,
          action: 'terminal_connect',
          resource: 'rig',
          resourceId: rigId,
          success: false,
          error: err.message,
        });
          socket.send(createSafeWSMessage('error', { message: 'SSH connection failed' }));
          socket.close();
        });

        sshClient.on('close', () => {
          socket.send(createSafeWSMessage('disconnected', { reason: 'SSH connection closed' }));
        });

        // Connect
        const config: Record<string, unknown> = {
          host: rig.sshCredential.host,
          port: rig.sshCredential.port,
          username: rig.sshCredential.username,
          readyTimeout: 10000,
        };

        if (privateKey) {
          config.privateKey = privateKey;
        } else if (password) {
          config.password = password;
        }

        sshClient.connect(config as Parameters<typeof sshClient.connect>[0]);
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

          // Handle authentication first
          if (message.type === 'auth' && !authenticated) {
            const token = message.token as string;
            if (!token || typeof token !== 'string') {
              socket.send(createSafeWSMessage('error', { message: 'Token required' }));
              return;
            }
            await authenticateAndConnect(token);
            return;
          }

          // All other messages require authentication
          if (!authenticated) {
            socket.send(createSafeWSMessage('error', { message: 'Not authenticated' }));
            return;
          }

          // Handle terminal input
          if (message.type === 'input') {
            const stream = (socket as unknown as { sshStream: NodeJS.WritableStream }).sshStream;
            const data = message.data as string;
            if (stream && typeof data === 'string') {
              // Limit input size to prevent abuse
              if (data.length > 4096) {
                socket.send(createSafeWSMessage('error', { message: 'Input too large' }));
                return;
              }
              stream.write(data);
            }
          }

          // Handle terminal resize
          if (message.type === 'resize') {
            const stream = (socket as unknown as { sshStream: { setWindow: (rows: number, cols: number, height: number, width: number) => void } }).sshStream;
            const rows = message.rows as number;
            const cols = message.cols as number;
            
            if (stream && stream.setWindow && 
                typeof rows === 'number' && typeof cols === 'number' &&
                rows > 0 && rows < 1000 && cols > 0 && cols < 1000) {
              stream.setWindow(rows, cols, rows * 20, cols * 10);
            }
          }

        } catch (error) {
          console.error('Terminal WebSocket error:', error);
          socket.send(createSafeWSMessage('error', { message: 'Internal error' }));
        }
      });

      socket.on('close', () => {
        clearTimeout(authTimeout);
        if (sshClient) {
          sshClient.end();
        }
        if (userId) {
          auditLog({
            userId,
            action: 'terminal_disconnect',
            resource: 'rig',
            resourceId: rigId,
            success: true,
          });
        }
      });

      socket.on('error', (error: Error) => {
        console.error('Terminal WebSocket error:', error);
        clearTimeout(authTimeout);
        if (sshClient) {
          sshClient.end();
        }
      });
    }
  );
}
