import { Box } from '@mantine/core';

export default function FullscreenFrame({
  fullscreen,
  parent,
  children,
}: {
  fullscreen?: boolean;
  parent?: HTMLElement | null;
  children: React.ReactNode;
}) {
  if (!fullscreen) return <>{children}</>;

  return (
    <Box
      style={
        parent
          ? {
              width: '100%',
              height: 'auto',
              maxHeight: 'none',
              overflow: 'visible',
            }
          : {
              width: 'min(96vw, calc(100vw - 3rem))',
              maxHeight: 'none',
              overflow: 'visible',
            }
      }
    >
      {children}
    </Box>
  );
}
