import type { Response } from '@/lib/api/response';
import { Button, LoadingOverlay, NumberInput, Stack, Switch, Text, TextInput } from '@mantine/core';
import { useForm } from '@mantine/form';
import { IconDeviceFloppy } from '@tabler/icons-react';
import { useNavigate } from 'react-router-dom';
import { settingsOnSubmit } from '../settingsOnSubmit';
import useServerSettings from '../useServerSettings';

export default function Ratelimit() {
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

  const form = useForm<{
    ratelimitEnabled: boolean;
    ratelimitMax: number;
    ratelimitWindow: number | '' | null;
    ratelimitAdminBypass: boolean;
    ratelimitAllowList: string;
  }>({
    initialValues: {
      ratelimitEnabled: data.settings.ratelimitEnabled,
      ratelimitMax: data.settings.ratelimitMax,
      ratelimitWindow: data.settings.ratelimitWindow,
      ratelimitAdminBypass: data.settings.ratelimitAdminBypass,
      ratelimitAllowList: data.settings.ratelimitAllowList.join(', '),
    },
    enhanceGetInputProps: (payload: any): object => ({
      disabled:
        data.tampered.includes(payload.field) ||
        (payload.field !== 'ratelimitEnabled' && !form.values.ratelimitEnabled) ||
        false,
    }),
  });

  const onSubmit = async (values: typeof form.values) => {
    if (values.ratelimitAllowList?.trim() === '' || !values.ratelimitAllowList) {
      // @ts-ignore
      values.ratelimitAllowList = [];
    } else {
      // @ts-ignore
      values.ratelimitAllowList = values.ratelimitAllowList
        .split(',')
        .map((x) => x.trim())
        .filter((x) => x !== '');
    }

    if (values.ratelimitWindow === '') {
      // @ts-ignore
      values.ratelimitWindow = null;
    }

    return settingsOnSubmit(navigate, form)(values);
  };

  return (
    <>
      <Text size='sm' c='dimmed' mb='md'>
        All options require a restart to take effect.
      </Text>

      <form onSubmit={form.onSubmit(onSubmit)}>
        <Stack gap='lg'>
          <Switch
            label='Enable Ratelimit'
            description='Enable ratelimiting for the server.'
            {...form.getInputProps('ratelimitEnabled', { type: 'checkbox' })}
          />

          <Switch
            label='Admin Bypass'
            description='Allow admins to bypass the ratelimit.'
            {...form.getInputProps('ratelimitAdminBypass', { type: 'checkbox' })}
          />

          <NumberInput
            label='Max Requests'
            description='The maximum number of requests allowed within the window. If no window is set, this is the maximum number of requests until it reaches the limit.'
            placeholder='10'
            min={1}
            {...form.getInputProps('ratelimitMax')}
          />

          <NumberInput
            label='Window'
            description='The window in seconds to allow the max requests.'
            placeholder='60'
            min={1}
            {...form.getInputProps('ratelimitWindow')}
          />

          <TextInput
            label='Allow List'
            description='A comma-separated list of IP addresses to bypass the ratelimit.'
            placeholder='192.168.1.1, 127.0.0.1, 0.0.0.0'
            {...form.getInputProps('ratelimitAllowList')}
          />
        </Stack>

        <Button type='submit' mt='md' loading={isLoading} leftSection={<IconDeviceFloppy size='1rem' />}>
          Save
        </Button>
      </form>
    </>
  );
}
