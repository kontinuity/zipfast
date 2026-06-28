import type { Folder } from '@/lib/db/models/folder';
import { Badge, Button, Divider, Group, Modal, PasswordInput, Stack, Text } from '@mantine/core';
import { IconKey, IconLock, IconTrashFilled } from '@tabler/icons-react';
import { useEffect, useState } from 'react';
import { clearFolderPassword, setFolderPassword } from '../actions';

export default function EditFolderPasswordModal({
  folder,
  onClose,
  opened,
}: {
  folder: Folder | null;
  onClose: () => void;
  opened: boolean;
}) {
  const [password, setPassword] = useState('');
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (opened) setPassword('');
  }, [opened, folder]);

  if (!folder) return null;

  const handleSave = async () => {
    const trimmed = password.trim();
    // Only send the password field when the user actually entered something,
    // so we never accidentally clear an existing password.
    if (trimmed === '') {
      onClose();
      return;
    }

    setLoading(true);
    const { error } = await setFolderPassword(folder, trimmed);
    setLoading(false);

    if (!error) {
      setPassword('');
      onClose();
    }
  };

  const handleRemove = async () => {
    setLoading(true);
    const { error } = await clearFolderPassword(folder);
    setLoading(false);

    if (!error) {
      setPassword('');
      onClose();
    }
  };

  return (
    <Modal opened={opened} onClose={onClose} title={`Folder password for "${folder.name}"`}>
      <Stack gap='sm' my='sm'>
        {folder.passwordProtected && (
          <Group gap='xs'>
            <Badge color='yellow' variant='light' leftSection={<IconLock size='0.8rem' />}>
              Password protected
            </Badge>
          </Group>
        )}

        <PasswordInput
          label='Folder password'
          description="Set a password to protect this folder's public listing. Leave blank to keep unchanged."
          placeholder={folder.passwordProtected ? 'Enter a new password...' : 'Enter a password...'}
          value={password}
          autoComplete='off'
          onChange={(event) => setPassword(event.currentTarget.value)}
          leftSection={<IconKey size='1rem' />}
        />

        <Button onClick={handleSave} loading={loading} leftSection={<IconLock size='1rem' />}>
          Save password
        </Button>

        {folder.passwordProtected && (
          <>
            <Divider label='or' labelPosition='center' />
            <Text size='xs' c='dimmed'>
              Remove the password to make this folder&apos;s public listing accessible without a password.
            </Text>
            <Button
              variant='light'
              color='red'
              loading={loading}
              leftSection={<IconTrashFilled size='1rem' />}
              onClick={handleRemove}
            >
              Remove password
            </Button>
          </>
        )}
      </Stack>
    </Modal>
  );
}
