import type { Response } from '@/lib/api/response';
import { Button, LoadingOverlay, Stack, Switch, TextInput } from '@mantine/core';
import { useForm } from '@mantine/form';
import { IconDeviceFloppy } from '@tabler/icons-react';
import { useNavigate } from 'react-router-dom';
import { settingsOnSubmit } from '../settingsOnSubmit';
import useServerSettings from '../useServerSettings';

export default function Chunks() {
  const { data, isLoading } = useServerSettings();

  return (
    <>
      <LoadingOverlay visible={isLoading} bdrs='md' />
      {data ? <Form data={data} isLoading={isLoading} /> : null}
    </>
  );
}

function Form({ data, isLoading }: { data: Response['/api/server/settings']; isLoading: boolean }) {
  const navigate = useNavigate();

  const form = useForm({
    initialValues: {
      chunksEnabled: data.settings.chunksEnabled,
      chunksMax: data.settings.chunksMax,
      chunksSize: data.settings.chunksSize,
    },
    enhanceGetInputProps: (payload: any): object => ({
      disabled:
        data.tampered.includes(payload.field) ||
        (payload.field !== 'chunksEnabled' && !form.values.chunksEnabled) ||
        false,
    }),
  });

  const onSubmit = settingsOnSubmit(navigate, form);

  return (
    <form onSubmit={form.onSubmit(onSubmit)}>
      <Stack gap='lg'>
        <Switch
          label='Enable Chunks'
          description='Enable chunked uploads.'
          {...form.getInputProps('chunksEnabled', { type: 'checkbox' })}
        />

        <TextInput
          label='Max Chunk Size'
          description='Maximum size of an upload before it is split into chunks.'
          placeholder='95mb'
          disabled={!form.values.chunksEnabled}
          {...form.getInputProps('chunksMax')}
        />

        <TextInput
          label='Chunk Size'
          description='Size of each chunk.'
          placeholder='25mb'
          disabled={!form.values.chunksEnabled}
          {...form.getInputProps('chunksSize')}
        />
      </Stack>

      <Button type='submit' mt='md' loading={isLoading} leftSection={<IconDeviceFloppy size='1rem' />}>
        Save
      </Button>
    </form>
  );
}
