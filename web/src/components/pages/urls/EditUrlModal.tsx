import { Url } from '@/lib/db/models/url';
import { fetchApi } from '@/lib/fetchApi';
import useObjectState from '@/lib/client/hooks/useObjectState';
import { Button, Divider, Modal, NumberInput, PasswordInput, Stack, Switch, TextInput } from '@mantine/core';
import { showNotification } from '@mantine/notifications';
import { IconEye, IconKey, IconPencil, IconPencilOff, IconTrashFilled } from '@tabler/icons-react';
import { useEffect } from 'react';
import { mutate } from 'swr';

export default function EditUrlModal({ url, onClose }: { url: Url | null; onClose: () => void }) {
  const [urlData, setUrlData] = useObjectState<{
    maxViews: number | null;
    vanity: string | null;
    destination: string | null;
    enabled: boolean;
    password: string | null;
  }>({
    maxViews: url?.maxViews ?? null,
    vanity: url?.vanity ?? null,
    destination: url?.destination ?? null,
    enabled: url?.enabled ?? true,
    password: '',
  });

  useEffect(() => {
    if (url) {
      setUrlData({
        maxViews: url.maxViews,
        vanity: url.vanity,
        destination: url.destination,
        enabled: url.enabled,
        password: '',
      });
    }
  }, [url]);

  const handleRemovePassword = async () => {
    if (!url?.password) return;

    const { error } = await fetchApi(`/api/user/urls/${url.id}`, 'PATCH', {
      password: null,
    });

    if (error) {
      showNotification({
        title: 'Failed to remove password...',
        message: error.error,
        color: 'red',
        icon: <IconPencilOff size='1rem' />,
      });
    } else {
      showNotification({
        title: 'Password removed!',
        message: 'The password has been removed from the URL.',
        color: 'green',
        icon: <IconPencil size='1rem' />,
      });

      onClose();
      mutate('/api/user/urls');
      mutate({ key: '/api/user/urls' });
    }
  };

  const handleSave = async () => {
    if (!url) return;

    const data: {
      maxViews?: number | null;
      password?: string;
      vanity?: string;
      destination?: string;
      enabled?: boolean;
    } = {};

    if (urlData.maxViews === null) data['maxViews'] = null;
    else data['maxViews'] = urlData.maxViews;

    if (urlData.password !== null && urlData.password.trim() !== '')
      data['password'] = urlData.password?.trim();

    if (urlData.vanity !== null && urlData.vanity !== url.vanity) data['vanity'] = urlData.vanity?.trim();
    if (urlData.destination !== null && urlData.destination !== url.destination)
      data['destination'] = urlData.destination?.trim();
    if (urlData.enabled !== url.enabled) data['enabled'] = urlData.enabled;

    const { error } = await fetchApi(`/api/user/urls/${url.id}`, 'PATCH', data);

    if (error) {
      showNotification({
        title: 'Failed to save changes...',
        message: error.error,
        color: 'red',
        icon: <IconPencilOff size='1rem' />,
      });
    } else {
      showNotification({
        title: 'Changes saved!',
        message: 'The changes have been saved successfully.',
        color: 'green',
        icon: <IconPencil size='1rem' />,
      });

      onClose();
      mutate('/api/user/urls');
      mutate({ key: '/api/user/urls' });
    }
  };

  return (
    <Modal title={`Editing "${url?.vanity ?? url?.code ?? 'unknown'}"`} opened={!!url} onClose={onClose}>
      <Stack gap='xs' my='sm'>
        <NumberInput
          label='Max Views'
          placeholder='Unlimited'
          description='The maximum number of clicks this URL can have before it is automatically deleted. Leave blank to allow as many views as you want.'
          value={urlData.maxViews || ''}
          onChange={(value) => setUrlData('maxViews', value === '' ? null : Number(value))}
          min={0}
          leftSection={<IconEye size='1rem' />}
        />

        <TextInput
          label='Vanity'
          placeholder='Optional'
          description='A custom alias for your URL. Leave blank to use the randomly generated code.'
          value={urlData.vanity || ''}
          onChange={(event) =>
            setUrlData(
              'vanity',
              event.currentTarget.value.trim() === '' ? null : event.currentTarget.value.trim(),
            )
          }
        />

        <TextInput
          label='Destination'
          placeholder='https://example.com'
          value={urlData.destination || ''}
          onChange={(event) =>
            setUrlData(
              'destination',
              event.currentTarget.value.trim() === '' ? null : event.currentTarget.value.trim(),
            )
          }
        />

        <Switch
          label='Enabled'
          description='Prevent or allow this URL from being visited.'
          checked={urlData.enabled}
          onChange={(event) => setUrlData('enabled', event.currentTarget.checked)}
        />

        <Divider />

        {url?.password ? (
          <Button
            variant='light'
            color='red'
            leftSection={<IconTrashFilled size='1rem' />}
            onClick={handleRemovePassword}
          >
            Remove password
          </Button>
        ) : (
          <PasswordInput
            label='Password'
            description='Set a password for this URL. Leave blank to disable password protection.'
            value={urlData.password ?? ''}
            autoComplete='off'
            onChange={(event) =>
              setUrlData(
                'password',
                event.currentTarget.value.trim() === '' ? null : event.currentTarget.value.trim(),
              )
            }
            leftSection={<IconKey size='1rem' />}
          />
        )}

        <Divider />

        <Button onClick={handleSave} leftSection={<IconPencil size='1rem' />}>
          Save changes
        </Button>
      </Stack>
    </Modal>
  );
}
