import { FastifyInstance, FastifyRequest } from 'fastify';
import { Client } from 'ssh2';
import { prisma } from '@bloxos/database';
import { authService } from '../services/auth-service.ts';
import { decrypt } from '../utils/encryption.ts';

interface TerminalParams {
  rigId: string;
}

export async function terminalRoutes(app: FastifyInstance) {
  // WebSocket terminal connection
  app.get<{ Params: TerminalParams }>('/ws/:rigId', { websocket: true }, async (socket, request) => {
    const { rigId } = request.params;
    let sshClient: Client | null = null;
    let authenticated = false;

    socket.on('message', async (rawMessage: Buffer) => {
      try {
        const message = JSON.parse(rawMessage.toString());

        // Handle authentication first
        if (message.type === 'auth') {
          const token = message.token;
          const payload = authService.verifyToken(token);

          if (!payload) {
            socket.send(JSON.stringify({ type: 'error', message: 'Invalid token' }));
            socket.close();
            return;
          }

          authenticated = true;

          // Get rig with SSH credentials
          const rig = await prisma.rig.findUnique({
            where: { id: rigId },
            include: { sshCredential: true },
          });

          if (!rig) {
            socket.send(JSON.stringify({ type: 'error', message: 'Rig not found' }));
            socket.close();
            return;
          }

          if (!rig.sshCredential) {
            socket.send(JSON.stringify({ type: 'error', message: 'No SSH credentials configured' }));
            socket.close();
            return;
          }

          // Decrypt password
          if (!rig.sshCredential.encryptedPassword) {
            socket.send(JSON.stringify({ type: 'error', message: 'No password configured' }));
            socket.close();
            return;
          }
          const password = decrypt(rig.sshCredential.encryptedPassword);

          // Connect via SSH
          sshClient = new Client();

          sshClient.on('ready', () => {
            socket.send(JSON.stringify({ type: 'connected' }));

            sshClient!.shell({ term: 'xterm-256color', cols: 120, rows: 30 }, (err, stream) => {
              if (err) {
                socket.send(JSON.stringify({ type: 'error', message: err.message }));
                return;
              }

              // Send SSH output to WebSocket
              stream.on('data', (data: Buffer) => {
                socket.send(JSON.stringify({ type: 'output', data: data.toString('utf8') }));
              });

              stream.stderr.on('data', (data: Buffer) => {
                socket.send(JSON.stringify({ type: 'output', data: data.toString('utf8') }));
              });

              stream.on('close', () => {
                socket.send(JSON.stringify({ type: 'disconnected' }));
                socket.close();
              });

              // Store stream reference for input handling
              (socket as unknown as { sshStream: typeof stream }).sshStream = stream;
            });
          });

          sshClient.on('error', (err: Error) => {
            socket.send(JSON.stringify({ type: 'error', message: `SSH Error: ${err.message}` }));
            socket.close();
          });

          sshClient.on('close', () => {
            socket.send(JSON.stringify({ type: 'disconnected' }));
          });

          // Connect
          sshClient.connect({
            host: rig.sshCredential.host,
            port: rig.sshCredential.port,
            username: rig.sshCredential.username,
            password: password,
            readyTimeout: 10000,
          });

          return;
        }

        // Handle terminal input
        if (message.type === 'input' && authenticated) {
          const stream = (socket as unknown as { sshStream: NodeJS.WritableStream }).sshStream;
          if (stream) {
            stream.write(message.data);
          }
        }

        // Handle terminal resize
        if (message.type === 'resize' && authenticated) {
          const stream = (socket as unknown as { sshStream: { setWindow: (rows: number, cols: number, height: number, width: number) => void } }).sshStream;
          if (stream && stream.setWindow) {
            stream.setWindow(message.rows, message.cols, message.rows * 20, message.cols * 10);
          }
        }

      } catch (error) {
        console.error('Terminal WebSocket error:', error);
      }
    });

    socket.on('close', () => {
      if (sshClient) {
        sshClient.end();
      }
    });

    socket.on('error', (error: Error) => {
      console.error('Terminal WebSocket error:', error);
      if (sshClient) {
        sshClient.end();
      }
    });
  });
}
