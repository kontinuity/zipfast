import DashboardServerSettings from '@/components/pages/serverSettings';
import { useTitle } from '@/lib/client/hooks/useTitle';

export function Component() {
  useTitle('Server Settings');

  return <DashboardServerSettings />;
}

Component.displayName = 'Dashboard/Admin/Settings';
