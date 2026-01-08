import { FastifyInstance, FastifyRequest, FastifyReply } from 'fastify';
import { z } from 'zod';
import { prisma } from '@bloxos/database';
import { authService } from '../services/auth-service.ts';
import { notificationService } from '../services/notification-service.ts';

const NotificationSettingsSchema = z.object({
  emailEnabled: z.boolean().optional(),
  emailAddress: z.string().email().optional().nullable(),
  telegramEnabled: z.boolean().optional(),
  telegramChatId: z.string().optional().nullable(),
  notifyOnOffline: z.boolean().optional(),
  notifyOnHighTemp: z.boolean().optional(),
  notifyOnLowHashrate: z.boolean().optional(),
  notifyOnMinerError: z.boolean().optional(),
  tempThreshold: z.number().min(50).max(100).optional(),
  hashrateDropPercent: z.number().min(5).max(80).optional(),
});

const TestNotificationSchema = z.object({
  type: z.enum(['email', 'telegram']),
});

// Helper to get user ID from request
async function getUserId(request: FastifyRequest): Promise<string | null> {
  const token = request.cookies.token || request.headers.authorization?.replace('Bearer ', '');
  if (!token) return null;

  const payload = await authService.verifyToken(token);
  return payload?.userId || null;
}

export async function settingsRoutes(app: FastifyInstance) {
  // Get notification settings
  app.get('/notifications', async (request: FastifyRequest, reply: FastifyReply) => {
    const userId = await getUserId(request);
    if (!userId) {
      return reply.status(401).send({ error: 'Not authenticated' });
    }

    const settings = await prisma.notificationSettings.findUnique({
      where: { userId },
    });

    // Return defaults if no settings exist
    if (!settings) {
      return reply.send({
        emailEnabled: false,
        emailAddress: '',
        telegramEnabled: false,
        telegramChatId: '',
        notifyOnOffline: true,
        notifyOnHighTemp: true,
        notifyOnLowHashrate: true,
        notifyOnMinerError: true,
        tempThreshold: 85,
        hashrateDropPercent: 20,
      });
    }

    return reply.send({
      emailEnabled: settings.emailEnabled,
      emailAddress: settings.emailAddress || '',
      telegramEnabled: settings.telegramEnabled,
      telegramChatId: settings.telegramChatId || '',
      notifyOnOffline: settings.notifyOnOffline,
      notifyOnHighTemp: settings.notifyOnHighTemp,
      notifyOnLowHashrate: settings.notifyOnLowHashrate,
      notifyOnMinerError: settings.notifyOnMinerError,
      tempThreshold: settings.tempThreshold,
      hashrateDropPercent: settings.hashrateDropPercent,
    });
  });

  // Update notification settings
  app.put('/notifications', async (request: FastifyRequest, reply: FastifyReply) => {
    const userId = await getUserId(request);
    if (!userId) {
      return reply.status(401).send({ error: 'Not authenticated' });
    }

    const result = NotificationSettingsSchema.safeParse(request.body);
    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed', details: result.error.issues });
    }

    const data = result.data;

    // Validate email if enabling email notifications
    if (data.emailEnabled && !data.emailAddress) {
      return reply.status(400).send({ error: 'Email address is required when email notifications are enabled' });
    }

    // Validate telegram chat ID if enabling telegram
    if (data.telegramEnabled && !data.telegramChatId) {
      return reply.status(400).send({ error: 'Telegram Chat ID is required when Telegram notifications are enabled' });
    }

    try {
      const settings = await prisma.notificationSettings.upsert({
        where: { userId },
        create: {
          userId,
          emailEnabled: data.emailEnabled ?? false,
          emailAddress: data.emailAddress,
          telegramEnabled: data.telegramEnabled ?? false,
          telegramChatId: data.telegramChatId,
          notifyOnOffline: data.notifyOnOffline ?? true,
          notifyOnHighTemp: data.notifyOnHighTemp ?? true,
          notifyOnLowHashrate: data.notifyOnLowHashrate ?? true,
          notifyOnMinerError: data.notifyOnMinerError ?? true,
          tempThreshold: data.tempThreshold ?? 85,
          hashrateDropPercent: data.hashrateDropPercent ?? 20,
        },
        update: {
          emailEnabled: data.emailEnabled,
          emailAddress: data.emailAddress,
          telegramEnabled: data.telegramEnabled,
          telegramChatId: data.telegramChatId,
          notifyOnOffline: data.notifyOnOffline,
          notifyOnHighTemp: data.notifyOnHighTemp,
          notifyOnLowHashrate: data.notifyOnLowHashrate,
          notifyOnMinerError: data.notifyOnMinerError,
          tempThreshold: data.tempThreshold,
          hashrateDropPercent: data.hashrateDropPercent,
        },
      });

      return reply.send({
        emailEnabled: settings.emailEnabled,
        emailAddress: settings.emailAddress || '',
        telegramEnabled: settings.telegramEnabled,
        telegramChatId: settings.telegramChatId || '',
        notifyOnOffline: settings.notifyOnOffline,
        notifyOnHighTemp: settings.notifyOnHighTemp,
        notifyOnLowHashrate: settings.notifyOnLowHashrate,
        notifyOnMinerError: settings.notifyOnMinerError,
        tempThreshold: settings.tempThreshold,
        hashrateDropPercent: settings.hashrateDropPercent,
      });
    } catch (error) {
      console.error('Failed to save notification settings:', error);
      return reply.status(500).send({ error: 'Failed to save settings' });
    }
  });

  // Test notification
  app.post('/notifications/test', async (request: FastifyRequest, reply: FastifyReply) => {
    const userId = await getUserId(request);
    if (!userId) {
      return reply.status(401).send({ error: 'Not authenticated' });
    }

    const result = TestNotificationSchema.safeParse(request.body);
    if (!result.success) {
      return reply.status(400).send({ error: 'Validation failed' });
    }

    const { type } = result.data;

    // Get user's settings
    const settings = await prisma.notificationSettings.findUnique({
      where: { userId },
    });

    if (!settings) {
      return reply.status(400).send({ error: 'No notification settings configured' });
    }

    if (type === 'email') {
      if (!settings.emailEnabled || !settings.emailAddress) {
        return reply.status(400).send({ error: 'Email notifications not configured' });
      }

      const result = await notificationService.sendTest('email', settings.emailAddress);
      if (!result.success) {
        return reply.status(500).send({ error: result.error || 'Failed to send test email' });
      }

      return reply.send({ success: true, message: 'Test email sent' });
    }

    if (type === 'telegram') {
      if (!settings.telegramEnabled || !settings.telegramChatId) {
        return reply.status(400).send({ error: 'Telegram notifications not configured' });
      }

      const result = await notificationService.sendTest('telegram', settings.telegramChatId);
      if (!result.success) {
        return reply.status(500).send({ error: result.error || 'Failed to send test message' });
      }

      return reply.send({ success: true, message: 'Test Telegram message sent' });
    }

    return reply.status(400).send({ error: 'Invalid notification type' });
  });
}
