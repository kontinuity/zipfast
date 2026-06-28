import type { Response } from '@/lib/api/response';
import useSWR from 'swr';

type ApiPaginationOptions = {
  route?: string;
  page?: number;
  filter?: string;
  perpage?: number;
  favorite?: boolean;
  sort?:
    | 'name'
    | 'id'
    | 'createdAt'
    | 'updatedAt'
    | 'deletesAt'
    | 'originalName'
    | 'size'
    | 'type'
    | 'views'
    | 'favorite';
  order?: 'asc' | 'desc';
  id?: string;
  folderId?: string;
  search?: {
    field?: string;
    query: string;
  };
};

const fetcher = async <T,>(
  { options }: { options: ApiPaginationOptions; key: string } = {
    options: {
      page: 1,
    },
    key: '/api/user/files',
  },
): Promise<T> => {
  const route = options.route ?? '/api/user/files';
  const searchParams = new URLSearchParams();
  if (options.page) searchParams.append('page', options.page.toString());
  if (options.filter) searchParams.append('filter', options.filter);
  if (options.favorite) searchParams.append('favorite', options.favorite.toString());
  if (options.perpage) searchParams.append('perpage', options.perpage.toString());
  if (options.sort) searchParams.append('sortBy', options.sort);
  if (options.order) searchParams.append('order', options.order);
  if (options.id) searchParams.append('id', options.id);
  if (options.search) {
    if (options.search.field) searchParams.append('searchField', options.search.field);
    searchParams.append('searchQuery', options.search.query);
  }
  if (options.folderId) searchParams.append('folder', options.folderId);

  const res = await fetch(`${route}${searchParams.toString() ? `?${searchParams.toString()}` : ''}`);

  if (!res.ok) {
    const json = await res.json();

    throw new Error(json.message);
  }

  return res.json();
};

export function useApiPagination<T = Response['/api/user/files']>(
  options: ApiPaginationOptions = {
    page: 1,
  },
  swrConfig?: Parameters<typeof useSWR<T>>[2],
) {
  const { data, error, isLoading, mutate } = useSWR<T>(
    { key: options.route ?? '/api/user/files', options },
    {
      fetcher: (k) => fetcher<T>(k),
      ...swrConfig,
    },
  );

  return {
    data,
    error,
    isLoading,
    mutate,
  };
}
