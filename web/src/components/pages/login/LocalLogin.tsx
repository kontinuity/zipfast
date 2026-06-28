import { Stack, TextInput, PasswordInput, Button } from '@mantine/core';
import { UseFormReturnType } from '@mantine/form';

export default function LocalLogin({
  form,
  onSubmit,
  loading,
  hasBackground,
}: {
  form: UseFormReturnType<any>;
  onSubmit: (values: any) => void;
  loading: boolean;
  hasBackground: boolean;
}) {
  return (
    <form onSubmit={form.onSubmit((v) => onSubmit(v))}>
      <Stack my='sm'>
        <TextInput
          size='md'
          placeholder='Enter your username...'
          autoComplete='username'
          styles={{
            input: { backgroundColor: hasBackground ? 'transparent' : undefined },
          }}
          {...form.getInputProps('username')}
        />

        <PasswordInput
          size='md'
          placeholder='Enter your password...'
          autoComplete='current-password'
          styles={{
            input: { backgroundColor: hasBackground ? 'transparent' : undefined },
          }}
          {...form.getInputProps('password')}
        />

        <Button
          size='md'
          fullWidth
          type='submit'
          loading={loading}
          variant={hasBackground ? 'outline' : 'filled'}
        >
          Login
        </Button>
      </Stack>
    </form>
  );
}
