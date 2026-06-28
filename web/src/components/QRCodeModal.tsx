import { getDomain } from '@/lib/client/webDomain';
import { Button, Group, Image, Modal, Select, Text, Tooltip } from '@mantine/core';
import { showNotification } from '@mantine/notifications';
import { IconClipboardCheck, IconClipboardX, IconCopy, IconDownload } from '@tabler/icons-react';
import { useEffect, useState } from 'react';

type Type = 'image/png' | 'image/jpeg' | 'image/webp';

const UNSUPPORTED_COPY = ['image/jpeg', 'image/webp'];

export default function QRCodeModal({
  opened,
  onClose,
  url,
}: {
  opened: boolean;
  onClose: () => void;
  url: string;
}) {
  const [dataUrl, setDataUrl] = useState<string | null>(null);
  const [type, setType] = useState<Type>('image/png');

  useEffect(() => {
    if (!opened) return;

    import('qrcode')
      .then((QRCode) => QRCode.toDataURL(getDomain(url), { width: 500, type }))
      .then(setDataUrl)
      .catch(() => setDataUrl(null));
  }, [opened, url, type]);

  const copyImageToClipboard = async () => {
    if (!dataUrl) return;

    try {
      const response = await fetch(dataUrl);
      const blob = await response.blob();
      await navigator.clipboard.write([
        new ClipboardItem({
          [blob.type]: blob,
        }),
      ]);
      showNotification({
        message: 'QR code image copied to clipboard',
        color: 'green',
        icon: <IconClipboardCheck size='1rem' />,
      });
    } catch (error) {
      showNotification({
        title: 'Failed to copy QR code image',
        message: error instanceof Error ? error.message : String(error),
        color: 'red',
        icon: <IconClipboardX size='1rem' />,
      });
    }
  };

  const downloadImage = () => {
    if (!dataUrl) return;

    const link = document.createElement('a');
    link.href = dataUrl;
    link.style.display = 'none';
    link.download = `qr-code.${type.split('/')[1]}`;
    document.body.appendChild(link);
    link.click();
    document.body.removeChild(link);
  };

  return (
    <Modal title='QR Code' opened={opened} onClose={onClose} size='sm' centered>
      {dataUrl ? (
        <Image src={dataUrl} alt='QR Code' />
      ) : (
        <Text c='red' ta='center'>
          Failed to generate QR code.
        </Text>
      )}

      <Select
        mt='md'
        label='Format'
        value={type}
        onChange={(value) => setType(value as Type)}
        data={[
          { value: 'image/png', label: 'png' },
          { value: 'image/jpeg', label: 'jpeg' },
          { value: 'image/webp', label: 'webp' },
        ]}
        size='xs'
      />

      {dataUrl && (
        <Group gap='xs' mt='md' grow>
          <Tooltip
            label={
              UNSUPPORTED_COPY.includes(type)
                ? 'Copying this format is not supported in some browsers. You can copy the image normally via right-click or holding it.'
                : ''
            }
            hidden={!UNSUPPORTED_COPY.includes(type)}
          >
            <Button
              onClick={copyImageToClipboard}
              leftSection={<IconCopy size='1rem' />}
              disabled={UNSUPPORTED_COPY.includes(type)}
            >
              Copy Image
            </Button>
          </Tooltip>
          <Button onClick={downloadImage} leftSection={<IconDownload size='1rem' />}>
            Download
          </Button>
        </Group>
      )}
    </Modal>
  );
}
