import { useConfig } from '@/components/ConfigProvider';
import { Response } from '@/lib/api/response';
import { bytes } from '@/lib/bytes';
import { Anchor, Text } from '@mantine/core';
import { hideNotification, notifications } from '@mantine/notifications';
import { IconFileUpload, IconFileXFilled } from '@tabler/icons-react';
import { Link } from 'react-router-dom';
import { UploadProgress } from './useProgress';
import { applyUploadHeaders, handleUploadResponse, UploadHeadersOptions, UploadHandlers } from './shared';

export function progressTracker(size: number) {
  const alpha = 0.2;
  let totalBytes = 0;
  let resSpeed = 0;

  const startTime = Date.now();

  return {
    update: (loaded: number): UploadProgress => {
      const now = Date.now();
      const lastLoaded = totalBytes + loaded;

      const timeDiff = (now - startTime) / 1000;

      // exponential moving average
      if (timeDiff > 0) {
        const speed = lastLoaded / timeDiff;

        resSpeed = resSpeed === 0 ? speed : speed * alpha + resSpeed * (1 - alpha);
      }

      const percent = Math.round((lastLoaded / size) * 100);

      const remainingBytes = size - lastLoaded;
      const remaining = resSpeed > 0 ? remainingBytes / resSpeed : 0;

      return {
        percent: Math.min(percent, 99),
        speed: resSpeed,
        remaining: Math.max(remaining, 0),
      };
    },

    finish: (chunkSize: number) => {
      totalBytes += chunkSize;
    },
  };
}

export async function uploadPartialFiles(
  files: File[],
  {
    setProgress,
    setLoading,
    setFiles,
    clipboard,
    options,
    ephemeral,
    config,
    folder,
  }: UploadHandlers &
    UploadHeadersOptions & {
      clipboard: { copy: (text: string) => void };
      clearEphemeral?: () => void;
      config: ReturnType<typeof useConfig>;
    },
) {
  setLoading(true);
  setProgress({ percent: 0, remaining: 0, speed: 0 });

  const chunkSize = bytes(config.chunks.size);

  for (let i = 0; i !== files.length; ++i) {
    const file = files[i];

    const tracker = progressTracker(file.size);
    let lastUpdate = 0;

    const nChunks = Math.ceil(file.size / chunkSize);
    const chunks: {
      blob: Blob;
      start: number;
      end: number;
    }[] = [];

    for (let j = 0; j !== nChunks; ++j) {
      const start = j * chunkSize;
      const end = Math.min(start + chunkSize, file.size);
      chunks.push({
        blob: file.slice(start, end),
        start,
        end,
      });
    }

    notifications.show({
      id: 'upload-partial',
      title: 'Uploading partial file',
      message: `Uploading partial ${i + 1}/${chunks.length}`,
      loading: true,
      autoClose: false,
    });

    let ready = true;
    let identifier: string | undefined;

    for (let j = 0; j !== nChunks; ++j) {
      while (!ready) {
        await new Promise((resolve) => setTimeout(resolve, 100));
      }

      const body = new FormData();
      body.append('file', chunks[j].blob);

      setLoading(true);
      const req = new XMLHttpRequest();

      req.upload.addEventListener('progress', (e) => {
        if (!e.lengthComputable) return;

        const stats = tracker.update(e.loaded);

        const now = Date.now();
        if (now - lastUpdate > 250) {
          setProgress(stats);
          lastUpdate = now;
        }
      });

      req.addEventListener(
        'load',
        () => {
          const { data: res, error } = handleUploadResponse<Response['/api/upload/partial']>(req);

          if (error || !res) {
            notifications.update({
              id: 'upload-partial',
              title: 'Error uploading files',
              message: error?.error ?? 'An unknown error occurred',
              color: 'red',
              icon: <IconFileXFilled size='1rem' />,
              autoClose: false,
              loading: false,
            });
            ready = false;
            setFiles([]);
            setProgress({ percent: 0, remaining: 0, speed: 0 });
            setLoading(false);
            return;
          }

          notifications.update({
            id: 'upload-partial',
            title: 'Uploading partial file',
            message: `Uploading partial ${j + 1}/${nChunks} successful`,
            loading: false,
            autoClose: false,
          });

          if (j === 0) {
            identifier = res.partialIdentifier;
          }

          if (j === chunks.length - 1) {
            notifications.update({
              id: 'upload-partial',
              title: 'Finalizing partial upload',
              message: (
                <Text>
                  The upload has been offloaded and will complete in the background.
                  <br />
                  <Anchor
                    component='span'
                    onClick={() => {
                      hideNotification('upload-partial');
                      clipboard.copy(res.files[0].url);
                      notifications.show({
                        title: 'Copied URL to clipboard',
                        message: (
                          <Anchor component={Link} to={res.files[0].url} target='_blank'>
                            {res.files[0].url}
                          </Anchor>
                        ),
                      });
                    }}
                  >
                    Click here to copy the URL to clipboard while it&apos;s being processed.
                  </Anchor>
                  <br />
                  <Anchor component={Link} to='/dashboard/files?pending=true'>
                    View processing files
                  </Anchor>
                </Text>
              ),
              color: 'green',
              icon: <IconFileUpload size='1rem' />,
              autoClose: true,
              loading: false,
            });

            setFiles([]);
            setProgress({ percent: 100, remaining: 0, speed: 0 });
            setLoading(false);

            setTimeout(() => setProgress({ percent: 0, remaining: 0, speed: 0 }), 1000);
          }

          tracker.finish(chunks[j].blob.size);

          ready = true;
        },
        false,
      );

      req.open('POST', '/api/upload/partial');
      applyUploadHeaders(req, { options, ephemeral, folder });

      identifier && req.setRequestHeader('x-zipline-p-identifier', identifier);
      req.setRequestHeader('x-zipline-p-filename', encodeURIComponent(file.name));
      req.setRequestHeader('x-zipline-p-lastchunk', j === chunks.length - 1 ? 'true' : 'false');
      req.setRequestHeader('x-zipline-p-content-type', file.type);
      req.setRequestHeader('x-zipline-p-content-length', file.size.toString());
      req.setRequestHeader('content-range', `bytes ${chunks[j].start}-${chunks[j].end}/${file.size}`);

      req.send(body);

      ready = false;
    }
  }
}
