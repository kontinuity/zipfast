import { config } from '@/lib/config';
import { decrypt, encrypt } from '@/lib/crypto';

type AccessTokenPayload = {
  type: string;
  id: string;
  expiry: number;
};

export function createAccessToken({ type, id }: { type: string; id: string }): string {
  const payload: AccessTokenPayload = {
    type: type,
    id,
    expiry: Date.now() + 5 * 60_000, // 5 minutes
  };

  return encrypt(JSON.stringify(payload), config.core.secret);
}

export function verifyAccessToken(token: string | null | undefined, type: string, id: string): boolean {
  if (!token) return false;

  try {
    const raw = decrypt(token, config.core.secret);
    const payload = JSON.parse(raw) as Partial<AccessTokenPayload>;
    if (!payload || typeof payload !== 'object') return false;

    if (payload.type !== type) return false;
    if (payload.id !== id) return false;
    if (typeof payload.expiry !== 'number') return false;
    if (payload.expiry < Date.now()) return false;

    return true;
  } catch {
    return false;
  }
}
