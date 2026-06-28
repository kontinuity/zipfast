import type { Response } from '@/lib/api/response';
import { Button, Code, LoadingOverlay, Stack, Text, TextInput } from '@mantine/core';
import { useForm } from '@mantine/form';
import { IconDeviceFloppy } from '@tabler/icons-react';
import { useNavigate } from 'react-router-dom';
import { settingsOnSubmit } from '../settingsOnSubmit';
import useServerSettings from '../useServerSettings';

export default function Tasks() {
  const { data, isLoading } = useServerSettings();

  return (
    <>
      <LoadingOverlay visible={isLoading} />
      {data ? <Form data={data} isLoading={isLoading} /> : null}
    </>
  );
}

function Form({ data, isLoading }: { data: Response['/api/server/settings']; isLoading: boolean }) {
  const navigate = useNavigate();

  const form = useForm({
    initialValues: {
      tasksDeleteInterval: data.settings.tasksDeleteInterval,
      tasksClearInvitesInterval: data.settings.tasksClearInvitesInterval,
      tasksMaxViewsInterval: data.settings.tasksMaxViewsInterval,
      tasksThumbnailsInterval: data.settings.tasksThumbnailsInterval,
      tasksMetricsInterval: data.settings.tasksMetricsInterval,
      tasksCleanThumbnailsInterval: data.settings.tasksCleanThumbnailsInterval,
    },
    enhanceGetInputProps: (payload) => ({
      disabled: data.tampered.includes(payload.field) || false,
    }),
  });

  const onSubmit = settingsOnSubmit(navigate, form);

  return (
    <>
      <Text size='sm' c='dimmed' mb='md'>
        All options require a restart to take effect. Setting a value of <Code>0</Code> will disable the task.
      </Text>

      <form onSubmit={form.onSubmit(onSubmit)}>
        <Stack gap='lg'>
          <TextInput
            label='Delete Files Interval'
            description='How often to check and delete expired files.'
            placeholder='30m'
            {...form.getInputProps('tasksDeleteInterval')}
          />

          <TextInput
            label='Clear Invites Interval'
            description='How often to check and clear expired/used invites.'
            placeholder='30m'
            {...form.getInputProps('tasksClearInvitesInterval')}
          />

          <TextInput
            label='Max Views Interval'
            description='How often to check and delete files that have reached max views.'
            placeholder='30m'
            {...form.getInputProps('tasksMaxViewsInterval')}
          />

          <TextInput
            label='Thumbnails Interval'
            description='How often to check and generate thumbnails for video files.'
            placeholder='30m'
            {...form.getInputProps('tasksThumbnailsInterval')}
          />

          <TextInput
            label='Clean Thumbnails Interval'
            description='How often to check and delete orphaned thumbnails from the filesystem or database.'
            placeholder='1d'
            {...form.getInputProps('tasksCleanThumbnailsInterval')}
          />

          <TextInput
            label='Metrics Interval'
            description='How often to collect metrics data. Setting this to a lower value will give you more up-to-date metrics, but may increase CPU usage.'
            placeholder='30m'
            {...form.getInputProps('tasksMetricsInterval')}
          />
        </Stack>

        <Button type='submit' mt='md' loading={isLoading} leftSection={<IconDeviceFloppy size='1rem' />}>
          Save
        </Button>
      </form>
    </>
  );
}
