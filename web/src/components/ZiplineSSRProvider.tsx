import { createContext, useContext } from 'react';

export const ZiplineSSRContext = createContext<any>(null);

export function useSsrData<T>(): T {
  const ctx = useContext(ZiplineSSRContext);

  return ctx as T;
}

export default function ZiplineSSRProvider({
  children,
  ssrData,
}: {
  children: React.ReactNode;
  ssrData: any;
}) {
  return <ZiplineSSRContext.Provider value={ssrData}>{children}</ZiplineSSRContext.Provider>;
}
