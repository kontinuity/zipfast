import DashboardMetrics from '@/components/pages/metrics';
import { useTitle } from '@/lib/client/hooks/useTitle';
import { isAdministrator } from '@/lib/role';
import { redirect } from 'react-router-dom';

export async function loader() {
  const configRes = await fetch('/api/server/public');
  if (!configRes.ok) throw new Error('Failed to get public configuration');

  const config = await configRes.json();
  if (config.features.metrics?.adminOnly) {
    const res = await fetch('/api/user');
    if (!res.ok) return redirect('/auth/login');

    const { user } = await res.json();
    if (!isAdministrator(user.role)) return redirect('/dashboard');
  }

  return {};
}

export function Component() {
  useTitle('Metrics');

  return <DashboardMetrics />;
}

Component.displayName = 'Dashboard/Metrics';
