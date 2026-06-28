import { useTitle } from '@/lib/client/hooks/useTitle';
import { Button, Center, Stack, Text, Title } from '@mantine/core';
import { IconArrowLeft } from '@tabler/icons-react';
import { Link } from 'react-router-dom';

export default function FourOhFour() {
  useTitle('404');

  return (
    <Center h='100vh'>
      <Stack>
        <Title order={1}>404</Title>
        <Text c='dimmed' mt='-md'>
          Page not found
        </Text>

        <Button
          component={Link}
          to='/auth/login'
          color='blue'
          fullWidth
          leftSection={<IconArrowLeft size='1rem' />}
        >
          Go home
        </Button>
      </Stack>
    </Center>
  );
}
