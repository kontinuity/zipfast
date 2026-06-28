import { config } from '@/lib/config';
import typedPlugin from '../typedPlugin';

export const PATH = '/robots.txt';
export default typedPlugin(
  async (server) => {
    server.get(PATH, async (_, res) => {
      if (!config.features.robotsTxt) return res.callNotFound();

      return 'User-Agent: *\nDisallow: /';
    });
  },
  { name: PATH },
);
