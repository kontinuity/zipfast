import type { Response } from '@/lib/api/response';
import { Button, LoadingOverlay, NumberInput, Stack, TextInput } from '@mantine/core';
import { useForm } from '@mantine/form';
import { IconDeviceFloppy } from '@tabler/icons-react';
import { useNavigate } from 'react-router-dom';
import { settingsOnSubmit } from '../settingsOnSubmit';
import useServerSettings from '../useServerSettings';

export default function Urls() {
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
      urlsRoute: data.settings.urlsRoute,
      urlsLength: data.settings.urlsLength,
    },
    enhanceGetInputProps: (payload) => ({
      disabled: data.tampered.includes(payload.field) || false,
    }),
  });

  const onSubmit = settingsOnSubmit(navigate, form);

  return (
    <form onSubmit={form.onSubmit(onSubmit)}>
      <Stack gap='lg'>
        <TextInput
          label='Route'
          description='The route to use for short URLs. Requires a server restart.'
          placeholder='/go'
          {...form.getInputProps('urlsRoute')}
        />

        <NumberInput
          label='Length'
          description='The length of the short URL (for randomly generated names).'
          placeholder='6'
          min={1}
          max={64}
          {...form.getInputProps('urlsLength')}
        />
      </Stack>

      <Button type='submit' mt='md' loading={isLoading} leftSection={<IconDeviceFloppy size='1rem' />}>
        Save
      </Button>
    </form>
  );
}
