import { useUserStore } from '@/lib/client/store/user';
import { showNotification } from '@mantine/notifications';
import { IconLogout } from '@tabler/icons-react';
import { useNavigate } from 'react-router-dom';
import { mutate } from 'swr';

export function useLogout() {
  const setUser = useUserStore((state) => state.setUser);
  const navigate = useNavigate();

  const logout = async () => {
    showNotification({
      message: 'Logging out...',
      icon: <IconLogout size='1rem' />,
      autoClose: 700,
    });

    const res = await fetch('/api/auth/logout');
    if (res.ok) {
      setUser(null);
      await mutate('/api/user', null, false);
      navigate('/auth/login');
    }
  };

  return logout;
}
