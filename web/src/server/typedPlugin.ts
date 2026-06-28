import fastifyPlugin from 'fastify-plugin';
import type { FastifyPluginAsyncZod } from 'fastify-type-provider-zod';

export default function typedPlugin(
  plugin: FastifyPluginAsyncZod,
  opts?: Parameters<typeof fastifyPlugin>[1],
) {
  return fastifyPlugin(plugin as any, opts);
}
