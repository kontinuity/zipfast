import { Box } from '@mantine/core';

export default function FileZoomModal({
  setOpen,
  children,
}: {
  setOpen: (open: boolean) => void;
  children: React.ReactNode;
}) {
  return (
    <Box
      style={{
        position: 'fixed',
        top: 0,
        left: 0,
        width: '100vw',
        height: '100vh',
        background: 'rgba(0, 0, 0, 0.6)',
        backdropFilter: 'blur(calc(0.375rem * var(--mantine-scale)))',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        zIndex: 9999,
      }}
      onClick={() => setOpen(false)}
    >
      {children}
    </Box>
  );
}
