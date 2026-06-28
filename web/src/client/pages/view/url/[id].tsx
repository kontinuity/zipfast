import { useSsrData } from '@/components/ZiplineSSRProvider';
import { Anchor, Button, Modal, PasswordInput } from '@mantine/core';
import { useEffect, useState } from 'react';

export default function ViewUrlId() {
  const data = useSsrData<{
    url: { id: string; destination?: string };
    password?: boolean;
    token?: string | null;
  }>();
  if (!data) return null;

  const { url, password, token } = data;

  const [passwordValue, setPassword] = useState<string>('');
  const [passwordError, setPasswordError] = useState<string>('');

  useEffect(() => {
    if (!password && url.destination) window.location.href = url.destination;
  }, []);

  return password && !token ? (
    <Modal onClose={() => {}} opened={true} withCloseButton={false} centered title='Password required'>
      <form
        onSubmit={async (e) => {
          e.preventDefault();

          const res = await fetch(`/api/user/urls/${url.id}/password`, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ password: passwordValue.trim() }),
          });

          if (res.ok) {
            const json = (await res.json()) as { token: string };
            window.location.replace(`/view/url/${url.id}?token=${encodeURIComponent(json.token)}`);
          } else {
            setPasswordError('Invalid password');
          }
        }}
      >
        <PasswordInput
          description='This link is password protected, enter password to view it'
          required
          mb='sm'
          value={passwordValue}
          onChange={(event) => setPassword(event.currentTarget.value)}
          error={passwordError}
        />

        <Button
          fullWidth
          variant='outline'
          my='sm'
          type='submit'
          disabled={passwordValue.trim().length === 0}
        >
          Verify
        </Button>
      </form>
    </Modal>
  ) : (
    <p>
      Redirecting to <Anchor href={url.destination!}>{url.destination!}</Anchor>
    </p>
  );
}
