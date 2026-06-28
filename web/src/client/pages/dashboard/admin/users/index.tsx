import DashboardUsers from '@/components/pages/users';
import { useTitle } from '@/lib/client/hooks/useTitle';

export function Component() {
  useTitle('Users');

  return <DashboardUsers />;
}

Component.displayName = 'Dashboard/Admin/Users';
