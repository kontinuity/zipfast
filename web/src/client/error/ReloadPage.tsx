import { Button, Collapse, Container, Text, Title } from '@mantine/core';
import { IconReload } from '@tabler/icons-react';
import GenericError from './GenericError';
import { useState } from 'react';

export default function ReloadPage() {
  const [view, setView] = useState(false);

  return (
    <Container my='lg'>
      <Title order={3}>Update available</Title>

      <Text size='lg'>A new version of the app is available. Please reload the page to update.</Text>

      <Button
        leftSection={<IconReload size='1rem' />}
        mr='sm'
        mt='md'
        onClick={() => window.location.reload()}
      >
        Reload Page
      </Button>

      <Button variant='subtle' mt='md' onClick={() => setView((v) => !v)}>
        Why am I seeing this?
      </Button>

      <Collapse expanded={view}>
        <GenericError
          title='Failed to fetch dynamically imported module'
          message='This error can occur when a new version of the app is deployed while you have the page open. Please reload the page to update to the latest version.'
          details={{}}
        />
      </Collapse>
    </Container>
  );
}
