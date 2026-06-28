import { fetchUserInfo, type OAuthOptions, type OAuthUserInfoOptions } from '.';

export function githubAuthorizeURL({ clientId, state, redirectUri, origin }: OAuthOptions): string {
  const u = new URL('https://github.com/login/oauth/authorize');

  u.searchParams.set('client_id', clientId);
  u.searchParams.set('redirect_uri', redirectUri ?? `${origin}/api/auth/oauth/github`);
  u.searchParams.set('scope', 'read:user');

  if (state) u.searchParams.set('state', state);

  return u.toString();
}

export function githubUser(options: OAuthUserInfoOptions) {
  return fetchUserInfo({
    userInfoUrl: 'https://api.github.com/user',
    ...options,
  });
}
