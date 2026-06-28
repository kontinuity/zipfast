import { useRouteError } from 'react-router-dom';
import GenericError from './GenericError';
import ReloadPage from './ReloadPage';

export default function DashboardErrorBoundary(props: Record<string, any>) {
  const error = useRouteError();
  if (error instanceof Error && error.message.startsWith('Failed to fetch dynamically imported module:')) {
    return <ReloadPage />;
  }

  return (
    <GenericError
      title='Dashboard Client Error'
      message='Something went wrong while loading the dashboard. Please try again later, or report this issue if it persists.'
      details={{ ...props, type: 'dashboard' }}
    />
  );
}
