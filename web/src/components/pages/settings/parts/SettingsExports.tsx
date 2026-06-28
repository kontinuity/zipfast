import RelativeDate from '@/components/RelativeDate';
import { Response } from '@/lib/api/response';
import { bytes } from '@/lib/bytes';
import { ActionIcon, Button, Group, Paper, ScrollArea, Table, Text, Title, Tooltip } from '@mantine/core';
import { modals } from '@mantine/modals';
import { showNotification } from '@mantine/notifications';
import { IconDownload, IconPlus, IconTrashFilled } from '@tabler/icons-react';
import { Link } from 'react-router-dom';
import useSWR from 'swr';

export default function SettingsExports() {
  const { data, isLoading, mutate } = useSWR<Response['/api/user/export']>('/api/user/export', {
    refreshInterval: 5000,
  });

  const handleNewExport = async () => {
    modals.openConfirmModal({
      title: 'New export?',
      children:
        'Are you sure you want to start a new export? If you have a lot of files, this may take a while.',
      onConfirm: async () => {
        await fetch('/api/user/export', {
          method: 'POST',
        });

        showNotification({
          title: 'Export started',
          message: 'Export has been started, you can check its status in the table below',
          color: 'blue',
          loading: true,
        });
        mutate();
      },
      labels: {
        cancel: 'Cancel',
        confirm: 'Start export',
      },
    });
  };

  const handleDelete = async (id: string) => {
    await fetch(`/api/user/export?id=${id}`, {
      method: 'DELETE',
    });

    showNotification({
      message: 'Export has been deleted',
      color: 'red',
    });

    mutate();
  };

  return (
    <Paper withBorder p='sm'>
      <Title order={2}>Export Files</Title>

      <Button
        mt='sm'
        fullWidth
        disabled={isLoading}
        onClick={handleNewExport}
        leftSection={<IconPlus size='1rem' />}
      >
        New Export
      </Button>

      {data?.length === 0 ? (
        <Paper p='sm' mt='sm' withBorder>
          No exports found. Click the button above to start a new export.
        </Paper>
      ) : (
        <ScrollArea.Autosize mah={500} type='auto'>
          <Paper withBorder p={0} mt='sm'>
            <Table highlightOnHover stickyHeader>
              <Table.Thead>
                <Table.Tr>
                  <Table.Th>ID</Table.Th>
                  <Table.Th>Started</Table.Th>
                  <Table.Th>Files</Table.Th>
                  <Table.Th>Size</Table.Th>
                </Table.Tr>
              </Table.Thead>
              <Table.Tbody>
                {data?.map((exportDb) => (
                  <Table.Tr key={exportDb.id}>
                    <Table.Td maw={140}>
                      <Tooltip
                        label={`${exportDb.id} is ${exportDb.completed ? 'completed' : 'in progress'}`}
                      >
                        <Text
                          style={{ textOverflow: 'ellipsis', overflow: 'hidden' }}
                          c={exportDb.completed ? 'green' : 'dimmed'}
                        >
                          {exportDb.id}
                        </Text>
                      </Tooltip>
                    </Table.Td>
                    <Table.Td>
                      <RelativeDate date={new Date(exportDb.createdAt)} />
                    </Table.Td>
                    <Table.Td>{exportDb.files}</Table.Td>
                    <Table.Td>{exportDb.completed ? bytes(Number(exportDb.size)) : ''}</Table.Td>
                    <Table.Td w={95}>
                      <Group>
                        <ActionIcon onClick={() => handleDelete(exportDb.id)}>
                          <IconTrashFilled size='1rem' />
                        </ActionIcon>

                        <ActionIcon
                          component={Link}
                          target='_blank'
                          to={`/api/user/export?id=${exportDb.id}`}
                          disabled={!exportDb.completed}
                        >
                          <IconDownload size='1rem' />
                        </ActionIcon>
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </Table.Tbody>
            </Table>
          </Paper>
        </ScrollArea.Autosize>
      )}
    </Paper>
  );
}
