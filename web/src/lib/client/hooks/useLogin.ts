import type { Response } from '@/lib/api/response';
import { isAdministrator } from '@/lib/role';
import { useEffect } from 'react';
import { redirect } from 'react-router-dom';
import useSWR, { SWRConfiguration } from 'swr';
import { useShallow } from 'zustand/shallow';
import { useUserStore } from '../store/user';

export default function useLogin(
  { admin, swrConfig: swrOptions }: { admin?: boolean; swrConfig?: SWRConfiguration } = {
    admin: false,
    swrConfig: {},
  },
) {
  const { data, error, isLoading, mutate } = useSWR<Response['/api/user']>('/api/user', {
    fallbackData: { user: undefined },
    ...swrOptions,
  });

  const [user, setUser] = useUserStore(useShallow((state) => [state.user, state.setUser]));

  useEffect(() => {
    if (data?.user) {
      setUser(data.user);
    } else if (error) {
      redirect('/auth/login');
    }
  }, [data, error]);

  useEffect(() => {
    if (user && admin && !isAdministrator(user.role)) {
      redirect('/dashboard');
    }
  }, [user]);

  return { user, loading: isLoading || !user, mutate };
}
