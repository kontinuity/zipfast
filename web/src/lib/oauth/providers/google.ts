import { fetchUserInfo, type OAuthUserInfoOptions, type OAuthOptions } from '.';

export function googleAuthorizeURL({ clientId, origin, state, redirectUri }: OAuthOptions): string {
  const u = new URL('https://accounts.google.com/o/oauth2/auth');

  u.searchParams.set('client_id', clientId);
  u.searchParams.set('redirect_uri', redirectUri ?? `${origin}/api/auth/oauth/google`);
  u.searchParams.set('response_type', 'code');
  u.searchParams.set('access_type', 'offline');
  u.searchParams.set('scope', 'https://www.googleapis.com/auth/userinfo.profile');

  if (state) u.searchParams.set('state', state);

  return u.toString();
}

export function googleUser(options: OAuthUserInfoOptions) {
  return fetchUserInfo({
    userInfoUrl: 'https://www.googleapis.com/oauth2/v1/userinfo?alt=json',
    ...options,
  });
}
