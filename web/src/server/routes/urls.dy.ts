import { verifyAccessToken } from '@/lib/accessToken';
import { config } from '@/lib/config';
import { prisma } from '@/lib/db';
import { log } from '@/lib/logger';
import { FastifyReply, FastifyRequest } from 'fastify';

type Params = {
  id: string;
};

type Query = {
  token?: string;
};

const logger = log('server').c('urls');

export async function urlsRoute(
  req: FastifyRequest<{ Params: Params; Querystring: Query }>,
  res: FastifyReply,
) {
  const { id } = req.params;
  const { token } = req.query;

  const url = await prisma.url.findFirst({
    where: {
      OR: [{ code: id }, { vanity: id }, { id }],
    },
  });
  if (!url) return res.callNotFound();
  if (!url.enabled) return res.callNotFound();

  if (url.maxViews && url.views >= url.maxViews) {
    if (config.features.deleteOnMaxViews) {
      await prisma.url.delete({
        where: {
          id: url.id,
        },
      });

      logger.info(`${url.code} deleted due to reaching max views`, {
        id: url.id,
        views: url.views,
        vanity: url.vanity ?? 'none',
      });
    }

    return res.callNotFound();
  }

  if (url.password) {
    const valid = verifyAccessToken(token, 'url', url.id);
    if (!valid) return res.redirect(`/view/url/${url.id}`);
  }

  await prisma.url.update({
    where: {
      id: url.id,
    },
    data: {
      views: {
        increment: 1,
      },
    },
  });

  return res.redirect(url.destination);
}
