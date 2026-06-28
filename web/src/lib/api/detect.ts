import { detect } from 'detect-browser';
import z from 'zod';

const ziplineClientSchema = z.object({
  client: z.string(),
  device: z.string(),
  ua: z.string(),
});

export type ZiplineClient = z.infer<typeof ziplineClientSchema>;

export const ziplineClientParseSchema = z.string().transform((str, ctx) => {
  const parsed = parseZiplineClient(str);
  if (!parsed) {
    ctx.addIssue({
      code: 'custom',
      message: 'Invalid Zipline Client header',
    });
    return z.NEVER;
  }
  return parsed;
});

export function getWebClient(): ZiplineClient {
  if (typeof window === 'undefined') {
    return {
      client: 'unknown',
      device: 'unknown',
      ua: 'unknown',
    };
  }

  const ua = navigator.userAgent;
  const device = clientFromUA(ua);

  return {
    client: 'Zipline Web',
    device,
    ua,
  };
}

function parseZiplineClient(header: string | undefined): ZiplineClient | null {
  if (!header) return null;

  try {
    const parsed = JSON.parse(header);
    return ziplineClientSchema.parse(parsed);
  } catch {
    return null;
  }
}

function clientFromUA(ua: string): string {
  const detectedBrowser = detect(ua);

  const browser = detectedBrowser?.name ?? 'unknown';
  const version = detectedBrowser?.version ?? 'unknown';

  return `${browser} ${version}`;
}

export function detectClient(headers: Record<string, string | string[]>): ZiplineClient {
  const ua = <string>headers['user-agent'] ?? '';

  const header = <string>headers['x-zipline-client'];
  const ziplineClient = typeof header === 'object' ? header : parseZiplineClient(header);

  const detectedBrowser = detect(ua);

  const client = ziplineClient?.client ?? clientFromUA(ua);
  const device = ziplineClient?.device ?? detectedBrowser?.os ?? 'Web';

  return {
    client,
    device,
    ua,
  };
}
