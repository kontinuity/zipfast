import { z } from 'zod';
import { Config, discordContent } from '../config/validate';
import { ParseValue, parseString } from '../parser';
import { File } from '../db/models/file';
import { User } from '../db/models/user';
import { log } from '../logger';
import { Url } from '../db/models/url';
import { parserMetrics } from '../parser/metrics';

const logger = log('webhooks').c('discord');

export type DiscordContent = z.infer<typeof discordContent>;
export type WebhooksExecuteBody = {
  content?: string | null;
  username?: string | null;
  avatar_url?: string | null;
  embeds?:
    | {
        title?: string | null;
        description?: string | null;
        url?: string | null;
        timestamp?: string | null;
        color?: number | null;
        footer?: {
          text?: string;
        } | null;
        image?: {
          url?: string;
        } | null;
        thumbnail?: {
          url?: string;
        } | null;
        video?: {
          url?: string;
        } | null;
      }[]
    | null;
};

export function hexString(value?: string | null): number | null {
  if (!value) return null;

  const parsed = parseInt(value.replace(/^#/, ''), 16);
  return isNaN(parsed) ? null : parsed;
}

export function parseContent(
  config: Config,
  content: DiscordContent | null,
  value: ParseValue,
): (DiscordContent & { raw: string }) | null {
  if (!content) return null;
  if (!value.link) return null;

  return {
    content: content.content ? parseString(content.content, value) : null,
    embed: content.embed
      ? {
          color: content.embed.color,
          title: content.embed.title ? parseString(content.embed.title, value) : null,
          description: content.embed.description ? parseString(content.embed.description, value) : null,
          footer: content.embed.footer ? parseString(content.embed.footer, value) : null,
          timestamp: content.embed.timestamp,
          thumbnail: content.embed.thumbnail,
          imageOrVideo: content.embed.imageOrVideo,
          url: content.embed.url,
        }
      : null,
    avatarUrl:
      (config.discord?.avatarUrl ?? content.avatarUrl) ||
      'https://raw.githubusercontent.com/diced/zipline/9b60147e112ec5b70170500b85c75ea621f41d03/public/zipline.png',
    username: (config.discord?.username ?? content.username) || 'Zipline',
    webhookUrl: config.discord?.webhookUrl ?? content.webhookUrl,
    raw: value.link.raw || '{unknown_property}',
  };
}

export function buildResponse(
  content: ReturnType<typeof parseContent>,
  file?: File,
  url?: Partial<Url>,
): WebhooksExecuteBody | null {
  if (!content) return null;
  if (!file && !url) return null;

  const image = file ? file.type.startsWith('image/') : false;
  const video = file ? file.type.startsWith('video/') : false;

  return {
    username: content.username,
    avatar_url: content.avatarUrl,
    content: content.content,
    embeds: content.embed
      ? [
          {
            title: content.embed.title,
            description: content.embed.description,
            color: hexString(content.embed.color),
            timestamp: content.embed.timestamp ? (<Date>(file! || url!).createdAt).toISOString() : null,
            footer: content.embed.footer
              ? {
                  text: content.embed.footer,
                }
              : null,
            thumbnail: image && content.embed.thumbnail ? { url: content.raw } : null,
            image: image && content.embed.imageOrVideo ? { url: content.raw } : null,
            video: video && content.embed.imageOrVideo ? { url: content.raw } : null,
          },
        ]
      : null,
  };
}

export async function onUpload(
  config: Config,
  { user, file, link }: { user: User; file: File; link: ParseValue['link'] },
) {
  if (!config.discord?.onUpload) return logger.debug('no onUpload config, no webhook executed');

  const webhookUrl = config.discord?.onUpload?.webhookUrl || config.discord?.webhookUrl;
  if (!webhookUrl) return logger.debug('no webhookUrl config, no webhook executed');

  const metrics = await parserMetrics(user.id);

  const content = parseContent(config, config.discord?.onUpload, { user, file, link, ...metrics });
  if (!content) return logger.debug('no content somehow, no webhook executed');

  const response = buildResponse(content, file);
  if (!response) return logger.debug('no response somehow, no webhook executed');
  logger.c('onUpload').debug('sending webhook', { response: JSON.stringify(response) });

  const res = await fetch(content.webhookUrl!, {
    method: 'POST',
    body: JSON.stringify(response),
    headers: {
      'Content-Type': 'application/json',
    },
  });

  if (!res.ok) {
    const text = await res.text();
    logger.c('onUpload').error('webhook failed', { response: text, status: res.status });
  }

  return;
}

export async function onShorten(
  config: Config,
  {
    user,
    url,
    link,
  }: {
    user: User;
    url: Partial<Url>;
    link: ParseValue['link'];
  },
) {
  if (!config.discord?.onShorten) return logger.debug('no onShorten config, no webhook executed');

  const webhookUrl = config.discord?.onShorten?.webhookUrl || config.discord?.webhookUrl;
  if (!webhookUrl) return logger.debug('no webhookUrl config, no webhook executed');

  const metrics = await parserMetrics(user.id);

  const content = parseContent(config, config.discord?.onShorten, { user, url, link, ...metrics });
  if (!content) return logger.debug('no content somehow, no webhook executed');

  const response = buildResponse(content, undefined, url);
  if (!response) return logger.debug('no response somehow, no webhook executed');
  logger.c('onShorten').debug('sending webhook', { response: JSON.stringify(response) });

  const res = await fetch(content.webhookUrl!, {
    method: 'POST',
    body: JSON.stringify(response),
    headers: {
      'Content-Type': 'application/json',
    },
  });

  if (!res.ok) {
    const text = await res.text();
    logger.c('onShorten').error('webhook failed', { response: text, status: res.status });
  }

  return;
}
