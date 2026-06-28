import { ZIPLINE_SSR_PROP } from './constants';
import { uneval } from 'devalue';

function strip(obj: any): any {
  if (obj === null || typeof obj !== 'object') return obj;
  if (obj instanceof Date) return obj;

  if (Array.isArray(obj)) return obj.map((item) => strip(item));

  const stripped: Record<string, any> = {};
  for (const key of Object.keys(obj)) stripped[key] = strip(obj[key]);

  return stripped;
}

export function createZiplineSsr(data: any) {
  return `<script>window.${ZIPLINE_SSR_PROP} = ${uneval(strip(data))};</script>`;
}
