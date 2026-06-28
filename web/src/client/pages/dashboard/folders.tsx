import DashboardFolders from '@/components/pages/folders';
import { useTitle } from '@/lib/client/hooks/useTitle';

export function Component() {
  useTitle('Folders');

  return <DashboardFolders />;
}

Component.displayName = 'Dashboard/Folders';
