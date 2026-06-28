import { useReducer } from 'react';

export type UpdateFn<T extends object> = {
  <K extends keyof T>(key: K, value: T[K]): void;
  (obj: Partial<T>): void;
};

export default function useObjectState<T extends object>(initialState: T): [T, UpdateFn<T>] {
  const [state, dispatch] = useReducer((prevState: T, action: { type: 'update'; payload: Partial<T> }) => {
    switch (action.type) {
      case 'update':
        return { ...prevState, ...action.payload };
      default:
        return prevState;
    }
  }, initialState);

  const updateState: UpdateFn<T> = (keyOrObj: any, value?: any) => {
    if (typeof keyOrObj === 'object' && value === undefined) {
      dispatch({ type: 'update', payload: keyOrObj });
    } else if (typeof keyOrObj === 'string') {
      dispatch({ type: 'update', payload: { [keyOrObj]: value } as Partial<T> });
    }
  };

  return [state, updateState as UpdateFn<T>];
}
