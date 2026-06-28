import { config } from '@/lib/config';
import { log } from '@/lib/logger';
import { Worker } from 'worker_threads';

/**
 * Handles `{ type: 'query' }` messages coming from a thumbnail worker. The worker proxies its
 * database access to the main thread (see `src/offload/proxiedDb.ts`) so that workers don't each
 * load a full Prisma client.
 */
export type ThumbnailQueryHandler = (worker: Worker, message: any) => void | Promise<void>;

/**
 * A lazily-spawned pool of thumbnail worker threads.
 *
 * Previously the server spawned `features.thumbnails.num_threads` (default 4) worker threads at
 * boot and kept them alive forever. Each worker is a full V8 isolate that also loads ffmpeg and the
 * datasource (incl. the AWS SDK), so an idle server paid 100MB+ for workers that usually had nothing
 * to do. This pool instead spawns workers only when there is thumbnail work to do and shuts them
 * down again after a short idle grace period, so idle memory stays near zero.
 */
export class ThumbnailPool {
  private workers: Worker[] = [];
  private busy = 0;
  private idleTimer: NodeJS.Timeout | null = null;
  private logger = log('tasks').c('thumbnails');

  public constructor(
    private readonly numThreads: number,
    private readonly workerPath: string,
    private readonly onQuery: ThumbnailQueryHandler,
    private readonly idleGraceMs: number = 30_000,
  ) {}

  public get running(): boolean {
    return this.workers.length > 0;
  }

  private ensureSpawned(): void {
    if (this.workers.length) return;

    const count = Math.max(1, this.numThreads);
    for (let i = 0; i !== count; ++i) {
      const worker = new Worker(this.workerPath, {
        workerData: {
          id: `thumbnail-${i}`,
          enabled: true,
          config,
        },
      });

      worker.on('message', (message: any) => {
        if (message?.type === 'query') {
          void this.onQuery(worker, message);
        } else if (message?.type === 'done') {
          this.onBatchDone();
        }
      });

      worker.once('error', (err) => {
        this.logger.error('thumbnail worker error', { err: err.message });
        this.onBatchDone();
      });

      worker.once('exit', () => {
        this.workers = this.workers.filter((w) => w !== worker);
      });

      this.workers.push(worker);
    }

    this.logger.debug('spawned thumbnail workers', { count: this.workers.length });
  }

  /**
   * Distribute the given file ids across the worker pool (spawning it if necessary), then arrange
   * for the pool to shut down once all dispatched batches report completion.
   */
  public dispatch(fileIds: string[]): void {
    const unique = [...new Set(fileIds)].filter(Boolean);
    if (!unique.length) return;

    this.clearIdleTimer();
    this.ensureSpawned();

    const batches: string[][] = this.workers.map(() => []);
    unique.forEach((id, index) => {
      batches[index % this.workers.length].push(id);
    });

    for (let i = 0; i !== this.workers.length; ++i) {
      if (!batches[i].length) continue;

      this.busy++;
      this.workers[i].postMessage({ type: 0, data: batches[i] });
    }

    if (this.busy === 0) this.scheduleShutdown();
  }

  private onBatchDone(): void {
    this.busy = Math.max(0, this.busy - 1);
    if (this.busy === 0) this.scheduleShutdown();
  }

  private scheduleShutdown(): void {
    this.clearIdleTimer();
    this.idleTimer = setTimeout(() => this.shutdown(), this.idleGraceMs);
    // don't keep the event loop alive solely for this teardown timer
    this.idleTimer.unref?.();
  }

  private clearIdleTimer(): void {
    if (this.idleTimer) {
      clearTimeout(this.idleTimer);
      this.idleTimer = null;
    }
  }

  /** Gracefully ask all workers to exit and forget them; the next dispatch will respawn. */
  public shutdown(): void {
    this.clearIdleTimer();
    if (!this.workers.length) return;

    this.logger.debug('shutting down idle thumbnail workers', { count: this.workers.length });
    for (const worker of this.workers) {
      try {
        worker.postMessage({ type: 1 }); // graceful exit (handled in src/offload/thumbnails.ts)
      } catch {
        void worker.terminate();
      }
    }

    this.workers = [];
    this.busy = 0;
  }
}
