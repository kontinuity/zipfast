import type { Response } from '@/lib/api/response';
import { Button, LoadingOverlay, Stack, TextInput } from '@mantine/core';
import { useForm } from '@mantine/form';
import { IconDeviceFloppy } from '@tabler/icons-react';
import { useNavigate } from 'react-router-dom';
import { settingsOnSubmit } from '../settingsOnSubmit';
import useServerSettings from '../useServerSettings';

export default function HttpWebhook() {
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
      httpWebhookOnUpload: data.settings.httpWebhookOnUpload,
      httpWebhookOnShorten: data.settings.httpWebhookOnShorten,
    },
    enhanceGetInputProps: (payload) => ({
      disabled: data.tampered.includes(payload.field) || false,
    }),
  });

  const onSubmit = async (values: typeof form.values) => {
    for (const key in values) {
      if ((values[key as keyof typeof form.values] as string)?.trim() === '') {
        // @ts-ignore
        values[key as keyof typeof form.values] = null;
      } else {
        // @ts-ignore
        values[key as keyof typeof form.values] = (values[key as keyof typeof form.values] as string)?.trim();
      }
    }

    return settingsOnSubmit(navigate, form)(values);
  };

  return (
    <form onSubmit={form.onSubmit(onSubmit)}>
      <Stack gap='lg'>
        <TextInput
          label='On Upload'
          description='The URL to send a POST request to when a file is uploaded.'
          placeholder='https://example.com/upload'
          {...form.getInputProps('httpWebhookOnUpload')}
        />

        <TextInput
          label='On Shorten'
          description='The URL to send a POST request to when a URL is shortened.'
          placeholder='https://example.com/shorten'
          {...form.getInputProps('httpWebhookOnShorten')}
        />
      </Stack>

      <Button type='submit' mt='md' loading={isLoading} leftSection={<IconDeviceFloppy size='1rem' />}>
        Save
      </Button>
    </form>
  );
}
