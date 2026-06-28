import UploadFile from '@/components/pages/upload/File';
import { useTitle } from '@/lib/client/hooks/useTitle';

export function Component() {
  useTitle('Upload File');

  return <UploadFile />;
}

Component.displayName = 'Dashboard/Upload/File';
