import { Response } from '@/lib/api/response';
import { fetchApi } from '@/lib/fetchApi';
import { useLogout } from '@/lib/client/hooks/useLogout';
import { ActionIcon, Button, Modal, Paper, SimpleGrid, Skeleton, Table, Text, Title } from '@mantine/core';
import { modals } from '@mantine/modals';
import { showNotification } from '@mantine/notifications';
import { IconLogout, IconTrashFilled, IconUsers } from '@tabler/icons-react';
import { useState } from 'react';
import useSWR from 'swr';

export default function SettingsSessions() {
  const logout = useLogout();

  const { data, isLoading, mutate } = useSWR<Response['/api/user/sessions']>('/api/user/sessions');

  const [open, setOpen] = useState(false);

  const handleLogOutOfAllDevices = async () => {
    modals.openConfirmModal({
      title: 'Log out of all devices?',
      children:
        'Are you sure you want to log out of all devices? This will log you out of all devices except the current one.',
      onConfirm: async () => {
        const { error } = await fetchApi('/api/user/sessions', 'DELETE', {
          all: true,
        });

        if (!error) {
          showNotification({
            message: 'Logged out of all devices',
            color: 'blue',
            icon: <IconLogout size='1rem' />,
          });
        }
        mutate();
      },
      labels: {
        cancel: 'Cancel',
        confirm: 'Log out',
      },
    });
  };

  const handleLogOutOfDevice = async (sessionId: string) => {
    modals.openConfirmModal({
      title: 'Log out of device?',
      children: 'Are you sure you want to log out of this device?',
      onConfirm: async () => {
        const { error } = await fetchApi('/api/user/sessions', 'DELETE', {
          sessionId,
        });

        if (!error) {
          showNotification({
            message: 'Logged out of device',
            color: 'blue',
            icon: <IconLogout size='1rem' />,
          });
        }
        mutate();
      },
      labels: {
        cancel: 'Cancel',
        confirm: 'Log out',
      },
    });
  };

  const tableRows = data?.other.map((element) => (
    <Table.Tr key={element.id}>
      <Table.Td>{element.client}</Table.Td>
      <Table.Td>{element.device}</Table.Td>
      <Table.Td>{new Date(element.createdAt).toLocaleString()}</Table.Td>
      <Table.Td>
        <ActionIcon color='red' onClick={() => handleLogOutOfDevice(element.id)}>
          <IconTrashFilled size='1rem' />
        </ActionIcon>
      </Table.Td>
    </Table.Tr>
  ));

  return (
    <>
      <Modal title='Sessions' opened={open} onClose={() => setOpen(false)} size='lg'>
        <Paper withBorder>
          {data?.other?.length ? (
            <Table>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>Client</Table.Th>
                  <Table.Th>Device</Table.Th>
                  <Table.Th>Logged in at</Table.Th>
                  <Table.Th></Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>{tableRows}</Table.Tbody>
            </Table>
          ) : (
            <Text c='dimmed' p='md'>
              No other sessions found
            </Text>
          )}
        </Paper>

        <Button
          fullWidth
          mt='sm'
          color='yellow'
          onClick={handleLogOutOfAllDevices}
          disabled={!data?.other?.length}
        >
          Log out of all devices
        </Button>
      </Modal>

      <Paper withBorder p='sm'>
        <Title order={2}>Sessions</Title>

        <Skeleton visible={isLoading} animate mt='sm'>
          <Text c='dimmed'>
            You are currently logged into {isLoading ? '...' : (data?.other?.length ?? '...')} other devices
          </Text>
        </Skeleton>

        <SimpleGrid
          cols={{
            xs: 1,
            sm: 2,
          }}
          mt='sm'
        >
          <Button
            onClick={() => setOpen(true)}
            disabled={isLoading || !data?.other?.length}
            leftSection={<IconUsers size='1rem' />}
          >
            View sessions
          </Button>

          <Button color='yellow' onClick={logout} leftSection={<IconLogout size='1rem' />}>
            Log out of this browser
          </Button>
        </SimpleGrid>
      </Paper>
    </>
  );
}
