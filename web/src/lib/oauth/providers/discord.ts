import { fetchUserInfo, type OAuthOptions, type OAuthUserInfoOptions } from '.';

export function discordAuthorizeURL({ clientId, origin, state, redirectUri }: OAuthOptions): string {
  const u = new URL('https://discord.com/api/oauth2/authorize');

  u.searchParams.set('client_id', clientId);
  u.searchParams.set('redirect_uri', redirectUri ?? `${origin}/api/auth/oauth/discord`);
  u.searchParams.set('response_type', 'code');
  u.searchParams.set('scope', 'identify');
  u.searchParams.set('prompt', 'none');

  if (state) u.searchParams.set('state', state);

  return u.toString();
}

export function discordUser(options: OAuthUserInfoOptions) {
  return fetchUserInfo({
    userInfoUrl: 'https://discord.com/api/users/@me',
    ...options,
  });
}
