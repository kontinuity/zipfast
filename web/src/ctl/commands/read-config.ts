import { reloadSettings } from '@/lib/config';

export async function readConfig({ format }: { format: boolean }) {
  await reloadSettings();

  const { config } = await import('@/lib/config/index.js');

  console.log(JSON.stringify(config, null, format ? 2 : 0));
  process.exit(0);
}
