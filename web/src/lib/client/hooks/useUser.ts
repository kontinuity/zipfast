import type { Response } from '@/lib/api/response';
import useSWR from 'swr';

async function fetcher(url: string): Promise<Response['/api/user'] | null> {
  const res = await fetch(url);
  if (!res.ok) return null;

  return res.json();
}

export default function useUser(): {
  user: Response['/api/user']['user'] | undefined;
  loading: boolean;
} {
  const { data, isLoading } = useSWR<Response['/api/user'] | null>('/api/user', fetcher, {
    revalidateOnFocus: false,
    revalidateOnReconnect: false,
    refreshWhenHidden: false,
    revalidateIfStale: false,
    shouldRetryOnError: false,
  });

  return { user: data?.user, loading: isLoading };
}
