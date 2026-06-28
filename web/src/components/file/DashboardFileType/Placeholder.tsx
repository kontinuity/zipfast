import { Center, Stack, Text } from '@mantine/core';
import type { Icon } from '@tabler/icons-react';

export default function Placeholder({
  text,
  Icon,
  ...props
}: {
  text: string;
  Icon: Icon;
  onClick?: () => void;
}) {
  return (
    <Center py='xs' style={{ height: '100%', width: '100%', cursor: 'pointer' }} {...props}>
      <Stack align='center'>
        <Icon size='4rem' stroke={2} style={{ filter: 'drop-shadow(0 0 10px rgba(0, 0, 0, 0.9))' }} />
        <Text size='md' ta='center'>
          {text}
        </Text>
      </Stack>
    </Center>
  );
}
