import type { Response } from '@/lib/api/response';
import { Button, Divider, LoadingOverlay, Stack, Switch, TextInput } from '@mantine/core';
import { useForm } from '@mantine/form';
import { IconDeviceFloppy } from '@tabler/icons-react';
import { useNavigate } from 'react-router-dom';
import { settingsOnSubmit } from '../settingsOnSubmit';
import useServerSettings from '../useServerSettings';

export default function Mfa() {
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
      mfaTotpEnabled: data.settings.mfaTotpEnabled,
      mfaTotpIssuer: data.settings.mfaTotpIssuer,
      mfaPasskeysEnabled: data.settings.mfaPasskeysEnabled,
      mfaPasskeysRpID: data.settings.mfaPasskeysRpID,
      mfaPasskeysOrigin: data.settings.mfaPasskeysOrigin,
    },
    enhanceGetInputProps: (payload) => ({
      disabled: data.tampered.includes(payload.field) || false,
    }),
  });

  const onSubmit = settingsOnSubmit(navigate, form);

  return (
    <form onSubmit={form.onSubmit(onSubmit)}>
      <Stack gap='lg'>
        <Switch
          label='Passkeys'
          description='Enable the use of passwordless login with the use of WebAuthn passkeys like your phone, security keys, etc.'
          {...form.getInputProps('mfaPasskeysEnabled', { type: 'checkbox' })}
        />

        <TextInput
          label='Relying Party ID'
          description='The Relying Party ID (RP ID) to use for WebAuthn passkeys.'
          placeholder='example.com'
          {...form.getInputProps('mfaPasskeysRpID')}
        />

        <TextInput
          label='Origin'
          description='The Origin to use for WebAuthn passkeys.'
          placeholder='https://example.com'
          {...form.getInputProps('mfaPasskeysOrigin')}
        />

        <Divider />

        <Switch
          label='Enable TOTP'
          description='Enable Time-based One-Time Passwords with the use of an authenticator app.'
          {...form.getInputProps('mfaTotpEnabled', { type: 'checkbox' })}
        />
        <TextInput
          label='Issuer'
          description='The issuer to use for the TOTP token.'
          placeholder='Zipline'
          {...form.getInputProps('mfaTotpIssuer')}
        />
      </Stack>

      <Button type='submit' mt='md' loading={isLoading} leftSection={<IconDeviceFloppy size='1rem' />}>
        Save
      </Button>
    </form>
  );
}
