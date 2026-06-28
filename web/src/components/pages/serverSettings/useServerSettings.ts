import type { Response } from '@/lib/api/response';
import useSWR from 'swr';

export default function useServerSettings() {
  return useSWR<Response['/api/server/settings']>('/api/server/settings');
}
