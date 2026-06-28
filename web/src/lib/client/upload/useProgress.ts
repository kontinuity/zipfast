import useObjectState from '@/lib/client/hooks/useObjectState';
import { useCallback } from 'react';

export type UploadProgress = {
  percent: number;
  remaining: number;
  speed: number;
};

export const initialState: UploadProgress = {
  percent: 0,
  remaining: 0,
  speed: 0,
};

export function useProgress() {
  const [progress, updateProgress] = useObjectState<UploadProgress>(initialState);

  const setProgress = useCallback(
    (next: UploadProgress) => {
      updateProgress(next);
    },
    [updateProgress],
  );

  return [progress, setProgress] as const;
}
