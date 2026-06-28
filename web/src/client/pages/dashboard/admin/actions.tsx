import DashboardServerActions from '@/components/pages/serverActions';
import { useTitle } from '@/lib/client/hooks/useTitle';

export function Component() {
  useTitle('Server Actions');

  return <DashboardServerActions />;
}

Component.displayName = 'Dashboard/Admin/Actions';
