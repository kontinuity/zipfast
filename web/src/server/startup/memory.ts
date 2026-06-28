import { appendFile, writeFile } from 'fs/promises';

export async function startMemoryLog() {
  await writeFile('.memory.log', '', 'utf8');

  setInterval(async () => {
    const mu = process.memoryUsage();
    const cpu = process.cpuUsage();

    const entry = `${Math.floor(Date.now() / 1000)},${mu.rss},${mu.heapUsed},${mu.heapTotal},${mu.external},${mu.arrayBuffers},${cpu.system},${cpu.user}\n`;
    await appendFile('.memory.log', entry, 'utf8');
  }, 1000);
}
