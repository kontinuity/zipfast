import { Box, LoadingOverlay } from '@mantine/core';
import { useEffect, useRef, useState } from 'react';
import { useLocation } from 'react-router-dom';

export default function Asciinema({ src }: { src: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const [loaded, setLoaded] = useState(false);

  const location = useLocation();

  useEffect(() => {
    let cancelled = false;

    const loadPlayer = async () => {
      const AsciinemaPlayer = await import('asciinema-player');
      await import('asciinema-player/dist/bundle/asciinema-player.css');

      if (ref.current && !cancelled) {
        ref.current.innerHTML = '';

        AsciinemaPlayer.create(src, ref.current);
        setLoaded(true);
      }
    };

    loadPlayer();

    return () => {
      cancelled = true;
      if (ref.current) {
        ref.current.innerHTML = '';
      }
    };
  }, [src]);

  return (
    <div>
      {!loaded && (
        <Box pos='relative' h={400}>
          <LoadingOverlay visible />
        </Box>
      )}

      <div style={location.pathname.startsWith('/view') ? { width: '70vw' } : undefined} ref={ref} />
    </div>
  );
}
