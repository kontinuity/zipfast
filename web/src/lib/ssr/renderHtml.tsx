import { renderToString } from 'react-dom/server';
import { createStaticHandler, createStaticRouter, RouteObject, StaticRouterProvider } from 'react-router-dom';
import { createZiplineSsr } from './createZiplineSsr';

export async function renderHtml(
  routes: RouteObject[],
  { url, data, status, redirect }: { url: string; data: any; status: number; redirect?: string },
) {
  const { query } = createStaticHandler(routes);

  const context = await query(
    new Request('http://client' + url, {
      method: 'GET',
      headers: new Headers({ accept: 'text/html' }),
    }),
  );

  if (context instanceof Response)
    return {
      html: await context.text(),
    };

  const router = createStaticRouter(routes, context);
  const html = renderToString(<StaticRouterProvider context={context} router={router} />);

  return {
    html,
    meta: `<title>Shortened URL</title>\n${createZiplineSsr(data)}`,
    status,
    redirect,
  };
}
