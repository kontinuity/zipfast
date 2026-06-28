import { config } from '@/lib/config';
import { decrypt, encrypt } from '@/lib/crypto';

export type OAuthStateJSON = {
  mode: 'default' | 'link';
};

export function encryptOAuthState(value: OAuthStateJSON): string {
  return encrypt(JSON.stringify(value), config.core.secret);
}

export function decryptOAuthState(state?: string): string | null {
  if (!state) return null;

  try {
    return decrypt(decodeURIComponent(state), config.core.secret);
  } catch {
    return null;
  }
}

export function parseOAuthState(state?: string): OAuthStateJSON | null {
  const decrypted = decryptOAuthState(state);
  if (!decrypted) return null;

  // legacy
  if (decrypted === 'link') return { mode: 'link' };
  if (decrypted === 'default') return { mode: 'default' };

  try {
    const parsed = JSON.parse(decrypted) as Partial<OAuthStateJSON>;
    if (parsed?.mode !== 'default' && parsed?.mode !== 'link') return null;

    return {
      mode: parsed.mode,
    };
  } catch {
    return null;
  }
}
