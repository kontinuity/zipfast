import UploadText from '@/components/pages/upload/Text';
import { useTitle } from '@/lib/client/hooks/useTitle';

export function Component() {
  useTitle('Upload Text');

  return <UploadText />;
}

Component.displayName = 'Dashboard/Upload/Text';
