import { Container, Paper, ScrollArea, Stack, Text, Title } from '@mantine/core';
import { useRouteError } from 'react-router-dom';
import FourOhFour from '../pages/404';

export default function GenericError({
  title,
  message,
  details,
}: {
  title?: string;
  message?: string;
  details?: Record<string, any>;
}) {
  const routerError: any = useRouteError();
  if (routerError?.status === 404) return <FourOhFour />;

  const routeError = JSON.parse(JSON.stringify(routerError, Object.getOwnPropertyNames(routerError)));

  console.error(routerError);

  return (
    <Container my='lg'>
      <Stack gap='xs'>
        <Title order={5}>{title || 'An error occurred'}</Title>
        <Text c='dimmed'>
          {message || 'Something went wrong. Please try again later, or report this issue if it persists.'}
        </Text>
        {details && (
          <Paper withBorder px={3} py={3}>
            <ScrollArea>
              <pre style={{ margin: 0 }}>{JSON.stringify({ routeError, details }, null, 2)}</pre>
            </ScrollArea>
          </Paper>
        )}
      </Stack>
    </Container>
  );
}
