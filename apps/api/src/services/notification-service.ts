import { prisma } from '@bloxos/database';

interface NotificationPayload {
  title: string;
  message: string;
  severity: 'info' | 'warning' | 'error' | 'critical';
  rigId?: string;
  rigName?: string;
  data?: Record<string, unknown>;
}

interface SendResult {
  success: boolean;
  error?: string;
}

class NotificationService {
  private telegramBotToken: string | null = null;
  private smtpConfig: {
    host: string;
    port: number;
    secure: boolean;
    user: string;
    pass: string;
    from: string;
  } | null = null;

  constructor() {
    this.loadConfig();
  }

  private loadConfig() {
    // Telegram configuration
    this.telegramBotToken = process.env.TELEGRAM_BOT_TOKEN || null;

    // SMTP configuration
    const smtpHost = process.env.SMTP_HOST;
    if (smtpHost) {
      this.smtpConfig = {
        host: smtpHost,
        port: parseInt(process.env.SMTP_PORT || '587', 10),
        secure: process.env.SMTP_SECURE === 'true',
        user: process.env.SMTP_USER || '',
        pass: process.env.SMTP_PASS || '',
        from: process.env.SMTP_FROM || 'BloxOS <noreply@bloxos.local>',
      };
    }
  }

  /**
   * Send a notification to a user based on their settings
   */
  async notify(userId: string, payload: NotificationPayload): Promise<{ email: SendResult; telegram: SendResult }> {
    const settings = await prisma.notificationSettings.findUnique({
      where: { userId },
    });

    const results = {
      email: { success: false, error: 'Not configured' } as SendResult,
      telegram: { success: false, error: 'Not configured' } as SendResult,
    };

    if (!settings) {
      return results;
    }

    // Check if this notification type should be sent
    const shouldSend = this.shouldSendNotification(settings, payload);
    if (!shouldSend) {
      results.email = { success: true, error: 'Notification type disabled' };
      results.telegram = { success: true, error: 'Notification type disabled' };
      return results;
    }

    // Send email notification
    if (settings.emailEnabled && settings.emailAddress) {
      results.email = await this.sendEmail(settings.emailAddress, payload);
    }

    // Send Telegram notification
    if (settings.telegramEnabled && settings.telegramChatId) {
      results.telegram = await this.sendTelegram(settings.telegramChatId, payload);
    }

    return results;
  }

  /**
   * Check if notification should be sent based on user settings
   */
  private shouldSendNotification(
    settings: {
      notifyOnOffline: boolean;
      notifyOnHighTemp: boolean;
      notifyOnLowHashrate: boolean;
      notifyOnMinerError: boolean;
    },
    payload: NotificationPayload
  ): boolean {
    const title = payload.title.toLowerCase();

    if (title.includes('offline') && !settings.notifyOnOffline) return false;
    if (title.includes('temperature') && !settings.notifyOnHighTemp) return false;
    if (title.includes('hashrate') && !settings.notifyOnLowHashrate) return false;
    if (title.includes('error') && !settings.notifyOnMinerError) return false;

    return true;
  }

  /**
   * Send email notification
   */
  async sendEmail(to: string, payload: NotificationPayload): Promise<SendResult> {
    if (!this.smtpConfig) {
      console.log(`[Notification] Email not configured. Would send to ${to}: ${payload.title}`);
      return { success: true, error: 'SMTP not configured (simulated success)' };
    }

    try {
      // Dynamic import to avoid bundling nodemailer if not used
      const nodemailer = await import('nodemailer');

      const transporter = nodemailer.default.createTransport({
        host: this.smtpConfig.host,
        port: this.smtpConfig.port,
        secure: this.smtpConfig.secure,
        auth: {
          user: this.smtpConfig.user,
          pass: this.smtpConfig.pass,
        },
      });

      const severityEmoji = {
        info: 'ℹ️',
        warning: '⚠️',
        error: '❌',
        critical: '🚨',
      };

      const html = `
        <div style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; max-width: 600px; margin: 0 auto;">
          <div style="background: #1e293b; color: white; padding: 20px; border-radius: 8px 8px 0 0;">
            <h1 style="margin: 0; font-size: 24px;">${severityEmoji[payload.severity]} BloxOS Alert</h1>
          </div>
          <div style="background: #f8fafc; padding: 20px; border: 1px solid #e2e8f0; border-top: none;">
            <h2 style="margin: 0 0 10px; color: #1e293b;">${payload.title}</h2>
            <p style="margin: 0 0 15px; color: #475569; line-height: 1.5;">${payload.message}</p>
            ${payload.rigName ? `<p style="margin: 0; color: #64748b; font-size: 14px;"><strong>Rig:</strong> ${payload.rigName}</p>` : ''}
          </div>
          <div style="background: #f1f5f9; padding: 15px 20px; border-radius: 0 0 8px 8px; border: 1px solid #e2e8f0; border-top: none;">
            <p style="margin: 0; color: #64748b; font-size: 12px;">
              This is an automated notification from BloxOS. 
              <a href="#" style="color: #3b82f6;">Manage notifications</a>
            </p>
          </div>
        </div>
      `;

      await transporter.sendMail({
        from: this.smtpConfig.from,
        to,
        subject: `[BloxOS] ${payload.title}`,
        html,
        text: `${payload.title}\n\n${payload.message}${payload.rigName ? `\n\nRig: ${payload.rigName}` : ''}`,
      });

      console.log(`[Notification] Email sent to ${to}: ${payload.title}`);
      return { success: true };
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Unknown error';
      console.error(`[Notification] Email failed to ${to}:`, message);
      return { success: false, error: message };
    }
  }

  /**
   * Send Telegram notification
   */
  async sendTelegram(chatId: string, payload: NotificationPayload): Promise<SendResult> {
    if (!this.telegramBotToken) {
      console.log(`[Notification] Telegram not configured. Would send to ${chatId}: ${payload.title}`);
      return { success: true, error: 'Telegram bot token not configured (simulated success)' };
    }

    try {
      const severityEmoji = {
        info: 'ℹ️',
        warning: '⚠️',
        error: '❌',
        critical: '🚨',
      };

      const text = [
        `${severityEmoji[payload.severity]} *${this.escapeMarkdown(payload.title)}*`,
        '',
        this.escapeMarkdown(payload.message),
        payload.rigName ? `\n📍 *Rig:* ${this.escapeMarkdown(payload.rigName)}` : '',
      ].join('\n');

      const response = await fetch(`https://api.telegram.org/bot${this.telegramBotToken}/sendMessage`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          chat_id: chatId,
          text,
          parse_mode: 'MarkdownV2',
        }),
      });

      const data = await response.json() as { ok: boolean; description?: string };

      if (!data.ok) {
        throw new Error(data.description || 'Telegram API error');
      }

      console.log(`[Notification] Telegram sent to ${chatId}: ${payload.title}`);
      return { success: true };
    } catch (error) {
      const message = error instanceof Error ? error.message : 'Unknown error';
      console.error(`[Notification] Telegram failed to ${chatId}:`, message);
      return { success: false, error: message };
    }
  }

  /**
   * Escape special characters for Telegram MarkdownV2
   */
  private escapeMarkdown(text: string): string {
    return text.replace(/[_*[\]()~`>#+\-=|{}.!\\]/g, '\\$&');
  }

  /**
   * Send test notification
   */
  async sendTest(type: 'email' | 'telegram', destination: string): Promise<SendResult> {
    const payload: NotificationPayload = {
      title: 'Test Notification',
      message: 'This is a test notification from BloxOS. If you received this, notifications are working correctly!',
      severity: 'info',
    };

    if (type === 'email') {
      return this.sendEmail(destination, payload);
    } else {
      return this.sendTelegram(destination, payload);
    }
  }

  /**
   * Notify all users with enabled notifications for a specific rig
   */
  async notifyRigEvent(
    rigId: string,
    eventType: 'offline' | 'high_temp' | 'low_hashrate' | 'miner_error',
    payload: Omit<NotificationPayload, 'severity'>
  ) {
    // Get rig with farm and owner
    const rig = await prisma.rig.findUnique({
      where: { id: rigId },
      include: {
        farm: {
          include: {
            owner: {
              include: {
                notificationSettings: true,
              },
            },
          },
        },
      },
    });

    if (!rig || !rig.farm.owner.notificationSettings) {
      return;
    }

    const settings = rig.farm.owner.notificationSettings;

    // Check if this event type should trigger a notification
    let shouldNotify = false;
    let severity: NotificationPayload['severity'] = 'warning';

    switch (eventType) {
      case 'offline':
        shouldNotify = settings.notifyOnOffline;
        severity = 'error';
        break;
      case 'high_temp':
        shouldNotify = settings.notifyOnHighTemp;
        severity = 'warning';
        break;
      case 'low_hashrate':
        shouldNotify = settings.notifyOnLowHashrate;
        severity = 'warning';
        break;
      case 'miner_error':
        shouldNotify = settings.notifyOnMinerError;
        severity = 'error';
        break;
    }

    if (!shouldNotify) {
      return;
    }

    await this.notify(rig.farm.owner.id, {
      ...payload,
      severity,
      rigId: rig.id,
      rigName: rig.name,
    });
  }
}

export const notificationService = new NotificationService();
