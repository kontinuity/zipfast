import GenericError from './GenericError';

export default function RootErrorBoundary(props: Record<string, any>) {
  return (
    <GenericError
      title='Dashboard Client Error'
      message='Something went wrong while loading the dashboard. Please try again later, or report this issue if it persists.'
      details={{ ...props, type: 'root' }}
    />
  );
}
