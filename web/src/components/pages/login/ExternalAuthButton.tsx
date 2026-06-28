import { ActionIcon, Tooltip } from '@mantine/core';
import styles from './ExternalAuthButton.module.css';

export default function ExternalAuthButton({
  provider,
  leftSection,
}: {
  provider: string;
  leftSection: React.ReactNode;
}) {
  return (
    <Tooltip label={`Continue with ${provider}`}>
      <ActionIcon
        component={'a'}
        href={`/api/auth/oauth/${provider.toLowerCase()}`}
        color={`${provider.toLowerCase()}.0`}
        className={styles.button}
        p='lg'
        variant='oauth'
      >
        {leftSection}
      </ActionIcon>
    </Tooltip>
  );
}
