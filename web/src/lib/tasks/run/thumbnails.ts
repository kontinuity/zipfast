import { ThumbnailPool } from '@/lib/tasks/thumbnailPool';
import { IntervalTask } from '..';

export default function thumbnails(prisma: typeof globalThis.__db__, pool: ThumbnailPool) {
  return async function (this: IntervalTask, rerun = false) {
    if (rerun) this.logger.debug('regenerating thumbnails for all videos');

    const thumbnailNeeded = await prisma.file.findMany({
      where: {
        ...(rerun ? {} : { thumbnail: { is: null } }),

        type: {
          startsWith: 'video/',
        },
        size: { gt: 0 },
      },
      select: { id: true },
    });
    if (!thumbnailNeeded.length) return;

    this.logger.debug(`found ${thumbnailNeeded.length} files that need thumbnails`);

    pool.dispatch(thumbnailNeeded.map((x) => x.id));
  };
}
