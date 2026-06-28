import DashboardURLs from '@/components/pages/urls';
import { useTitle } from '@/lib/client/hooks/useTitle';

export function Component() {
  useTitle('URLs');

  return <DashboardURLs />;
}

Component.displayName = 'Dashboard/URLs';
