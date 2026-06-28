import { Anchor, Code, Modal, Text } from '@mantine/core';

export default function SecureWarningModal({
  returnHttps,
  opened,
  onClose,
}: {
  returnHttps: boolean;
  opened: boolean;
  onClose: () => void;
}) {
  return (
    <Modal opened={opened} onClose={onClose} title='HTTPS Configuration' size='lg'>
      <Text>
        {returnHttps ? (
          <>
            It appears that you are accessing this instance through an insecure context (HTTP), but the server
            is configured to use HTTPS. This can lead to issues when logging in, as secure cookies may not be
            sent by the browser.
          </>
        ) : (
          <>
            It appears that you are accessing this instance through a secure context (HTTPS), but the server
            is not configured to use HTTPS. This can lead issues when logging in.
          </>
        )}
      </Text>
      <Text mt='md'>
        {returnHttps ? (
          <>
            To resolve this issue, please access this instance through HTTPS. If that is currently not
            possible, you can temporarily set the <Code>CORE_RETURN_HTTPS_URLS</Code> environment variable to{' '}
            <Code>false</Code>.
          </>
        ) : (
          <>
            To resolve this issue, it is recommended to have your server configured to use HTTPS. This can be
            done by setting the <Code>CORE_RETURN_HTTPS_URLS</Code> environment variable to <Code>true</Code>{' '}
            and ensuring that your server has a valid SSL setup through a reverse proxy like Nginx or Caddy.
          </>
        )}
      </Text>

      <Text mt='md'>
        After making these changes, restart the server for the changes to take effect. If you continue to
        experience issues, please consult the{' '}
        <Anchor
          underline='always'
          href='https://zipline.diced.sh/docs/config/settings#more-about-return-https-urls'
        >
          documentation
        </Anchor>{' '}
        or seek support.
      </Text>
    </Modal>
  );
}
