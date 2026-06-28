import DashboardFiles from '@/components/pages/files';
import { useTitle } from '@/lib/client/hooks/useTitle';

export function Component() {
  useTitle('Files');

  return <DashboardFiles />;
}

Component.displayName = 'Dashboard/Files';
