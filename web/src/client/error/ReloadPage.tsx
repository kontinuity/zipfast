import { Button, Collapse, Container, Group, Loader, Text, Title } from '@mantine/core';
import { IconReload } from '@tabler/icons-react';
import GenericError from './GenericError';
import { useEffect, useState } from 'react';

// ReloadPage is shown when a lazily-imported route chunk fails to load — almost
// always because a new build was deployed while this tab was open, so the old
// asset hashes (e.g. /assets/folders-OLD.js) no longer exist.
//
// Rather than blocking the user with a manual prompt, we self-heal: reload once
// to fetch the fresh index.html (served no-cache) and its new chunk hashes. A
// short sessionStorage guard prevents an infinite loop — if the error recurs
// immediately (a genuinely broken deploy), we fall back to the manual prompt.
const RELOAD_GUARD_KEY = 'zf_chunk_reload_at';
const RELOAD_GUARD_WINDOW = 15000; // ms

export default function ReloadPage() {
  const [view, setView] = useState(false);
  const [autoReloading, setAutoReloading] = useState(false);

  useEffect(() => {
    try {
      const last = Number(window.sessionStorage.getItem(RELOAD_GUARD_KEY) ?? 0);
      if (Date.now() - last > RELOAD_GUARD_WINDOW) {
        window.sessionStorage.setItem(RELOAD_GUARD_KEY, String(Date.now()));
        setAutoReloading(true);
        window.location.reload();
      }
    } catch {
      /* sessionStorage unavailable (private mode) — show the manual prompt */
    }
  }, []);

  if (autoReloading) {
    return (
      <Container my='lg'>
        <Group gap='sm'>
          <Loader size='sm' />
          <Title order={3}>Updating…</Title>
        </Group>
        <Text size='lg' mt='xs'>
          Loading the latest version of the app.
        </Text>
      </Container>
    );
  }

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
