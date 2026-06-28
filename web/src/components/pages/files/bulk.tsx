import { mutateFiles } from '@/components/file/actions';
import { Response } from '@/lib/api/response';
import { getDomain } from '@/lib/client/webDomain';
import type { File } from '@/lib/db/models/file';
import { fetchApi } from '@/lib/fetchApi';
import { modals } from '@mantine/modals';
import { notifications } from '@mantine/notifications';
import {
  IconClipboardListFilled,
  IconFilesOff,
  IconStarsFilled,
  IconStarsOff,
  IconTrashFilled,
} from '@tabler/icons-react';

export async function bulkDelete(ids: string[], setSelectedFiles: (files: File[]) => void) {
  modals.openConfirmModal({
    centered: true,
    title: `Delete ${ids.length} file${ids.length === 1 ? '' : 's'}?`,
    children: `You are about to delete ${ids.length} file${
      ids.length === 1 ? '' : 's'
    }. This action cannot be undone.`,
    labels: {
      cancel: 'Cancel',
      confirm: 'Delete',
    },
    confirmProps: { color: 'red' },
    onConfirm: async () => {
      notifications.show({
        title: 'Deleting files',
        message: `Deleting ${ids.length} file${ids.length === 1 ? '' : 's'}`,
        color: 'blue',
        loading: true,
        id: 'bulk-delete',
        autoClose: false,
      });

      modals.closeAll();

      const { data, error } = await fetchApi<Response['/api/user/files/transaction']>(
        '/api/user/files/transaction',
        'DELETE',
        {
          files: ids,

          delete_datasourceFiles: true,
        },
      );

      if (error) {
        notifications.update({
          title: 'Error while deleting files',
          message: error.error,
          color: 'red',
          icon: <IconFilesOff size='1rem' />,
          id: 'bulk-delete',
          autoClose: true,
          loading: false,
        });
      } else if (data) {
        notifications.update({
          title: 'Deleted files',
          message: `Deleted ${data.count} file${ids.length === 1 ? '' : 's'}`,
          color: 'green',
          icon: <IconTrashFilled size='1rem' />,
          id: 'bulk-delete',
          autoClose: true,
          loading: false,
        });
      }

      setSelectedFiles([]);
      mutateFiles();
    },
    onCancel: modals.closeAll,
  });
}

export async function bulkFavorite(ids: string[], favorite: boolean) {
  const text = favorite ? 'favorite' : 'unfavorite';
  const textcaps = favorite ? 'Favorite' : 'Unfavorite';

  modals.openConfirmModal({
    centered: true,
    title: `${textcaps} ${ids.length} file${ids.length === 1 ? '' : 's'}?`,
    children: `You are about to ${text} ${ids.length} file${ids.length === 1 ? '' : 's'}.`,
    labels: {
      cancel: 'Cancel',
      confirm: `${textcaps}`,
    },
    confirmProps: { color: 'yellow' },
    onConfirm: async () => {
      notifications.show({
        title: `${textcaps}ing files`,
        message: `${textcaps}ing ${ids.length} file${ids.length === 1 ? '' : 's'}`,
        color: 'yellow',
        loading: true,
        id: 'bulk-favorite',
        autoClose: false,
      });
      modals.closeAll();

      const { data, error } = await fetchApi<Response['/api/user/files/transaction']>(
        '/api/user/files/transaction',
        'PATCH',
        {
          files: ids,

          favorite,
        },
      );

      if (error) {
        notifications.update({
          title: 'Error while modifying files',
          message: error.error,
          color: 'red',
          icon: <IconStarsOff size='1rem' />,
          id: 'bulk-favorite',
          autoClose: true,
          loading: false,
        });
      } else if (data) {
        notifications.update({
          title: `${textcaps}d files`,
          message: `${textcaps}d ${data.count} file${ids.length === 1 ? '' : 's'}`,
          color: 'yellow',
          icon: <IconStarsFilled size='1rem' />,
          id: 'bulk-favorite',
          autoClose: true,
          loading: false,
        });
      }

      mutateFiles();
    },
    onCancel: modals.closeAll,
  });
}

export async function bulkCopyLinks(urls: string[]) {
  const links = urls.map((url) => getDomain(url)).join('\n');

  await navigator.clipboard.writeText(links);

  notifications.show({
    title: 'Copied links to clipboard',
    message: `Copied ${urls.length} link${urls.length === 1 ? '' : 's'} to clipboard`,
    color: 'green',
    icon: <IconClipboardListFilled size='1rem' />,
    autoClose: true,
  });
}
