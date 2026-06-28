import { createHash, randomBytes } from 'crypto';

export function generatePKCEVerifier(size = 32): string {
  return randomBytes(size).toString('base64url');
}

export function generatePKCEChallenge(verifier: string): string {
  return createHash('sha256').update(verifier).digest('base64url');
}
