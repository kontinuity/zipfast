import type { Response } from '@/lib/api/response';
import { Button, LoadingOverlay, NumberInput, Stack, Switch } from '@mantine/core';
import { useForm } from '@mantine/form';
import { IconDeviceFloppy } from '@tabler/icons-react';
import { useNavigate } from 'react-router-dom';
import { settingsOnSubmit } from '../settingsOnSubmit';
import useServerSettings from '../useServerSettings';

export default function Invites() {
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
      invitesEnabled: data.settings.invitesEnabled,
      invitesLength: data.settings.invitesLength,
    },
    enhanceGetInputProps: (payload: any): object => ({
      disabled:
        data.tampered.includes(payload.field) ||
        (payload.field !== 'invitesEnabled' && !form.values.invitesEnabled) ||
        false,
    }),
  });

  const onSubmit = settingsOnSubmit(navigate, form);

  return (
    <form onSubmit={form.onSubmit(onSubmit)}>
      <Stack gap='lg'>
        <Switch
          label='Enable Invites'
          description='Enable the use of invite links to register new users.'
          {...form.getInputProps('invitesEnabled', { type: 'checkbox' })}
        />

        <NumberInput
          label='Length'
          description='The length of the invite code.'
          placeholder='6'
          min={1}
          max={64}
          {...form.getInputProps('invitesLength')}
        />
      </Stack>

      <Button type='submit' mt='md' loading={isLoading} leftSection={<IconDeviceFloppy size='1rem' />}>
        Save
      </Button>
    </form>
  );
}
