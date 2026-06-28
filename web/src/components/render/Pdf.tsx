import { useLocation } from 'react-router-dom';

export default function Pdf({ src }: { src: string }) {
  const location = useLocation();

  return (
    <iframe
      src={src + '#view=FitH'}
      style={{
        width: location.pathname.startsWith('/view') ? '70vw' : '100%',
        height: location.pathname.startsWith('/view') ? '80vh' : '100vh',
        border: 'none',
      }}
      title={src}
    />
  );
}
