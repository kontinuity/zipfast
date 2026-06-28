import typedPlugin from '../typedPlugin';

export const PATH = '/invite/:id';
export default typedPlugin(
  async (server) => {
    server.get<{ Params: { id: string } }>(PATH, async (req, res) => {
      const { id } = req.params;
      if (!id) return res.callNotFound();

      return res.redirect(`/auth/register?code=${encodeURIComponent(id)}`);
    });
  },
  { name: PATH },
);
