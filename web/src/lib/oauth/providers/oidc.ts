import { ApiError } from '@/lib/api/errors';
import { type OAuthOptions, type OAuthUserInfoOptions, fetchUserInfo } from '.';

export function oidcAuthorizeURL({
  authorizeUrl,
  clientId,
  origin,
  state,
  redirectUri,
  codeChallenge,
}: OAuthOptions): string {
  if (!authorizeUrl) throw new ApiError(2003);

  const u = new URL(authorizeUrl);

  u.searchParams.set('client_id', clientId);
  u.searchParams.set('redirect_uri', redirectUri ?? `${origin}/api/auth/oauth/oidc`);
  u.searchParams.set('response_type', 'code');
  u.searchParams.set('scope', 'openid email profile offline_access');

  if (state) u.searchParams.set('state', state);
  if (codeChallenge) {
    u.searchParams.set('code_challenge_method', 'S256');
    u.searchParams.set('code_challenge', codeChallenge);
  }

  return u.toString();
}

export function oidcUser(options: OAuthUserInfoOptions) {
  return fetchUserInfo(options);
}
