import fileIcon from '@/components/file/fileIcon';
import { useApiPagination } from '@/components/pages/files/useApiPagination';
import { type Response } from '@/lib/api/response';
import { bytes } from '@/lib/bytes';
import { useTitle } from '@/lib/client/hooks/useTitle';
import { Folder, PublicFolderFile } from '@/lib/db/models/folder';
import { FolderBreadcrumb } from '@/lib/folderHierarchy';
import {
  ActionIcon,
  Anchor,
  Box,
  Breadcrumbs,
  Button,
  Card,
  Center,
  Container,
  Group,
  Image,
  Pagination,
  Select,
  SegmentedControl,
  SimpleGrid,
  Stack,
  Table,
  Text,
  ThemeIcon,
  Title,
  Tooltip,
  UnstyledButton,
} from '@mantine/core';
import { useClipboard } from '@mantine/hooks';
import { notifications } from '@mantine/notifications';
import {
  IconArchive,
  IconArrowUp,
  IconChevronDown,
  IconChevronUp,
  IconCopy,
  IconDownload,
  IconFolder,
  IconLayoutGrid,
  IconLink,
  IconList,
  IconLockFilled,
  IconSelector,
  IconUpload,
} from '@tabler/icons-react';
import dayjs from 'dayjs';
import { useEffect, useState } from 'react';
import { Link, Params, useLoaderData, useNavigate, useSearchParams } from 'react-router-dom';
import { parseAsInteger, parseAsStringEnum, useQueryState } from 'nuqs';

import styles from './index.module.css';

type SortColumn = 'name' | 'createdAt' | 'updatedAt' | 'size';
type SortOrder = 'asc' | 'desc';
type FolderView = 'list' | 'grid';

const VIEW_STORAGE_KEY = 'zf_folder_view';
const PER_PAGE_OPTIONS = [9, 12, 15, 30, 45];

export async function loader({ params, request }: { params: Params<string>; request: Request }) {
  const url = new URL(request.url);
  const page = url.searchParams.get('page') ?? '1';
  const perpage = url.searchParams.get('perpage') ?? '15';
  const sortBy = url.searchParams.get('sortBy') ?? 'createdAt';
  const order = url.searchParams.get('order') ?? 'desc';

  const res = await fetch(
    `/api/server/folder/${params.id}?page=${encodeURIComponent(page)}&perpage=${encodeURIComponent(
      perpage,
    )}&sortBy=${encodeURIComponent(sortBy)}&order=${encodeURIComponent(order)}`,
  );
  if (!res.ok) {
    throw new Response('Folder not found', { status: 404 });
  }
  return {
    initial: (await res.json()) as Response['/api/server/folder/[id]'],
  };
}

// ---------------------------------------------------------------------------
// URL builders (origin = window.location.origin)
// ---------------------------------------------------------------------------

/** Public preview/open URL for a file (open in a new tab). */
function previewUrl(file: PublicFolderFile) {
  return file.url;
}

/** Force-download URL for a single file. */
function downloadUrl(file: PublicFolderFile) {
  return `/raw/${encodeURIComponent(file.name)}?download=true`;
}

/** Absolute, shareable link to a file (for the clipboard). */
function shareUrl(file: PublicFolderFile) {
  return `${window.location.origin}${file.url}`;
}

/** Public thumbnail URL (grid view) — only when the file has a thumbnail. */
function thumbnailUrl(file: PublicFolderFile) {
  return file.thumbnail?.path ? `/raw/${file.thumbnail.path}` : null;
}

/** "Download all" zip endpoint for a folder. */
function zipUrl(folderId: string) {
  return `/api/server/folder/${folderId}/zip`;
}

function displayNameOf(file: PublicFolderFile) {
  return file.displayName || file.originalName || file.name;
}

// ---------------------------------------------------------------------------
// Subfolder card (adapted from the previous PublicFolderCard)
// ---------------------------------------------------------------------------

function PublicFolderCard({ folder }: { folder: Partial<Folder> }) {
  return (
    <Card
      component={Link}
      to={`/folder/${folder.id}`}
      withBorder
      radius='md'
      className={styles.card}
      style={{ textDecoration: 'none', cursor: 'pointer' }}
    >
      <Group gap='sm' wrap='nowrap'>
        <ThemeIcon variant='light' size='lg' radius='md'>
          <IconFolder size='1.2rem' />
        </ThemeIcon>
        <Box style={{ minWidth: 0 }}>
          <Text fw={600} truncate>
            {folder.name}
          </Text>
          <Text size='xs' c='dimmed'>
            {folder._count?.files ?? 0} files
            {(folder._count?.children ?? 0) > 0 ? ` · ${folder._count?.children} subfolders` : ''}
          </Text>
        </Box>
      </Group>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Per-file actions (shared between list rows and grid cards)
// ---------------------------------------------------------------------------

function useCopyUrl() {
  const clipboard = useClipboard({ timeout: 1500 });

  return (url: string) => {
    clipboard.copy(url);
    notifications.show({
      title: 'Link copied',
      message: url,
      color: 'green',
      icon: <IconLink size='1rem' />,
    });
  };
}

/** Absolute, shareable link to a subfolder (for the clipboard). */
function folderShareUrl(folder: Partial<Folder>) {
  return `${window.location.origin}/folder/${folder.id}`;
}

/**
 * Sort subfolders client-side to mirror the active column. Folders have no size,
 * so a "size" sort falls back to name. Folders are always rendered above files.
 */
function sortFolders(folders: Partial<Folder>[], sortBy: SortColumn, order: SortOrder) {
  const dir = order === 'asc' ? 1 : -1;
  return [...folders].sort((a, b) => {
    if (sortBy === 'createdAt' || sortBy === 'updatedAt') {
      const av = new Date((a[sortBy] as string | Date | undefined) ?? 0).getTime();
      const bv = new Date((b[sortBy] as string | Date | undefined) ?? 0).getTime();
      return dir * (av - bv);
    }
    return dir * String(a.name ?? '').localeCompare(String(b.name ?? ''));
  });
}

function FileActions({ file, copyLink }: { file: PublicFolderFile; copyLink: (f: PublicFolderFile) => void }) {
  return (
    <Group gap={4} wrap='nowrap' justify='flex-end' onClick={(e) => e.stopPropagation()}>
      <Tooltip label='Copy link' withArrow>
        <ActionIcon variant='subtle' color='gray' onClick={() => copyLink(file)} aria-label='Copy link'>
          <IconLink size='1.1rem' />
        </ActionIcon>
      </Tooltip>

      <Tooltip label='Download' withArrow>
        <ActionIcon
          variant='subtle'
          color='gray'
          component='a'
          href={downloadUrl(file)}
          download
          aria-label='Download file'
        >
          <IconDownload size='1.1rem' />
        </ActionIcon>
      </Tooltip>
    </Group>
  );
}

// ---------------------------------------------------------------------------
// Subfolder row (list view) — rendered above files, like a file explorer
// ---------------------------------------------------------------------------

function FolderRow({
  folder,
  onOpen,
  onCopy,
}: {
  folder: Partial<Folder>;
  onOpen: (id: string) => void;
  onCopy: (folder: Partial<Folder>) => void;
}) {
  return (
    <Table.Tr className={styles.row} style={{ cursor: 'pointer' }} onClick={() => onOpen(folder.id!)}>
      <Table.Td>
        <Group gap='sm' wrap='nowrap' className={styles.nameButton}>
          <ThemeIcon variant='light' size='lg' radius='md' style={{ flexShrink: 0 }}>
            <IconFolder size='1.2rem' />
          </ThemeIcon>
          <Text size='sm' fw={600} truncate='end' style={{ minWidth: 0 }}>
            {folder.name}
          </Text>
        </Group>
      </Table.Td>
      <Table.Td visibleFrom='sm'>
        <Text size='sm' c='dimmed'>
          {folder.createdAt ? dayjs(folder.createdAt).format('MMM D, YYYY') : '—'}
        </Text>
      </Table.Td>
      <Table.Td visibleFrom='xs'>
        <Text size='sm' c='dimmed'>
          —
        </Text>
      </Table.Td>
      <Table.Td visibleFrom='md'>
        <Text size='sm' c='dimmed'>
          {folder.updatedAt ? dayjs(folder.updatedAt).format('MMM D, YYYY') : '—'}
        </Text>
      </Table.Td>
      <Table.Td>
        <Group gap={4} wrap='nowrap' justify='flex-end' onClick={(e) => e.stopPropagation()}>
          <Tooltip label='Copy link' withArrow>
            <ActionIcon
              variant='subtle'
              color='gray'
              onClick={() => onCopy(folder)}
              aria-label='Copy folder link'
            >
              <IconLink size='1.1rem' />
            </ActionIcon>
          </Tooltip>
          <Tooltip label='Download (zip)' withArrow>
            <ActionIcon
              variant='subtle'
              color='gray'
              component='a'
              href={zipUrl(folder.id!)}
              download
              aria-label='Download folder as zip'
            >
              <IconDownload size='1.1rem' />
            </ActionIcon>
          </Tooltip>
        </Group>
      </Table.Td>
    </Table.Tr>
  );
}

// ---------------------------------------------------------------------------
// Sortable table header cell
// ---------------------------------------------------------------------------

function SortableTh({
  label,
  column,
  sortBy,
  order,
  onSort,
  ...props
}: {
  label: string;
  column: SortColumn;
  sortBy: SortColumn;
  order: SortOrder;
  onSort: (column: SortColumn) => void;
} & React.ComponentPropsWithoutRef<typeof Table.Th>) {
  const active = sortBy === column;
  const Icon = active ? (order === 'asc' ? IconChevronUp : IconChevronDown) : IconSelector;

  return (
    <Table.Th {...props}>
      <UnstyledButton
        className={styles.sortable}
        onClick={() => onSort(column)}
        aria-label={`Sort by ${label}`}
      >
        <Group gap={4} wrap='nowrap'>
          <Text size='sm' fw={600} inherit>
            {label}
          </Text>
          <Icon size='0.9rem' color={active ? undefined : 'var(--mantine-color-dimmed)'} />
        </Group>
      </UnstyledButton>
    </Table.Th>
  );
}

// ---------------------------------------------------------------------------
// List view
// ---------------------------------------------------------------------------

function FileListView({
  files,
  folders,
  sortBy,
  order,
  onSort,
  copyLink,
  onOpenFolder,
  onCopyFolder,
}: {
  files: PublicFolderFile[];
  folders: Partial<Folder>[];
  sortBy: SortColumn;
  order: SortOrder;
  onSort: (column: SortColumn) => void;
  copyLink: (f: PublicFolderFile) => void;
  onOpenFolder: (id: string) => void;
  onCopyFolder: (folder: Partial<Folder>) => void;
}) {
  return (
    <Table.ScrollContainer minWidth={640}>
      <Table verticalSpacing='sm' highlightOnHover={false} layout='fixed'>
        <Table.Thead>
          <Table.Tr>
            <SortableTh label='File name' column='name' sortBy={sortBy} order={order} onSort={onSort} />
            <SortableTh
              label='Date added'
              column='createdAt'
              sortBy={sortBy}
              order={order}
              onSort={onSort}
              w={170}
              visibleFrom='sm'
            />
            <SortableTh
              label='Size'
              column='size'
              sortBy={sortBy}
              order={order}
              onSort={onSort}
              w={110}
              visibleFrom='xs'
            />
            <SortableTh
              label='Last update'
              column='updatedAt'
              sortBy={sortBy}
              order={order}
              onSort={onSort}
              w={170}
              visibleFrom='md'
            />
            <Table.Th w={130} ta='right'>
              <Text size='sm' fw={600}>
                Actions
              </Text>
            </Table.Th>
          </Table.Tr>
        </Table.Thead>
        <Table.Tbody>
          {folders.map((folder) => (
            <FolderRow
              key={`folder-${folder.id}`}
              folder={folder}
              onOpen={onOpenFolder}
              onCopy={onCopyFolder}
            />
          ))}
          {files.map((file) => {
            const Icon = fileIcon(file.type);
            return (
              <Table.Tr key={file.name} className={styles.row}>
                <Table.Td>
                  <Anchor
                    href={previewUrl(file)}
                    target='_blank'
                    rel='noopener noreferrer'
                    underline='never'
                    c='inherit'
                    className={styles.nameButton}
                  >
                    <ThemeIcon variant='light' size='lg' radius='md' style={{ flexShrink: 0 }}>
                      <Icon size='1.2rem' />
                    </ThemeIcon>
                    <Text size='sm' fw={500} truncate='end' style={{ minWidth: 0 }}>
                      {displayNameOf(file)}
                    </Text>
                    {file.password && (
                      <Tooltip label='Password protected' withArrow>
                        <IconLockFilled size='0.9rem' style={{ flexShrink: 0, opacity: 0.6 }} />
                      </Tooltip>
                    )}
                  </Anchor>
                </Table.Td>
                <Table.Td visibleFrom='sm'>
                  <Tooltip label={dayjs(file.createdAt).format('lll')} withArrow openDelay={300}>
                    <Text size='sm' c='dimmed'>
                      {dayjs(file.createdAt).format('MMM D, YYYY')}
                    </Text>
                  </Tooltip>
                </Table.Td>
                <Table.Td visibleFrom='xs'>
                  <Text size='sm' c='dimmed'>
                    {bytes(file.size)}
                  </Text>
                </Table.Td>
                <Table.Td visibleFrom='md'>
                  <Tooltip label={dayjs(file.updatedAt).format('lll')} withArrow openDelay={300}>
                    <Text size='sm' c='dimmed'>
                      {dayjs(file.updatedAt).format('MMM D, YYYY')}
                    </Text>
                  </Tooltip>
                </Table.Td>
                <Table.Td>
                  <FileActions file={file} copyLink={copyLink} />
                </Table.Td>
              </Table.Tr>
            );
          })}
        </Table.Tbody>
      </Table>
    </Table.ScrollContainer>
  );
}

// ---------------------------------------------------------------------------
// Grid view
// ---------------------------------------------------------------------------

function FileGridView({
  files,
  folders,
  copyLink,
}: {
  files: PublicFolderFile[];
  folders: Partial<Folder>[];
  copyLink: (f: PublicFolderFile) => void;
}) {
  return (
    <SimpleGrid cols={{ base: 1, xs: 2, sm: 3, lg: 4 }} spacing='md'>
      {folders.map((folder) => (
        <PublicFolderCard key={`folder-${folder.id}`} folder={folder} />
      ))}
      {files.map((file) => {
        const Icon = fileIcon(file.type);
        const thumb = thumbnailUrl(file);

        return (
          <Card key={file.name} withBorder radius='md' p='sm' className={styles.card}>
            <Card.Section>
              <Anchor
                href={previewUrl(file)}
                target='_blank'
                rel='noopener noreferrer'
                aria-label={`Open ${displayNameOf(file)}`}
              >
                <Box className={styles.thumb} style={{ height: 140 }}>
                  {thumb ? (
                    <Image src={thumb} alt={displayNameOf(file)} h={140} w='100%' fit='cover' />
                  ) : (
                    <Center h={140}>
                      <Icon size='2.5rem' opacity={0.55} />
                    </Center>
                  )}
                </Box>
              </Anchor>
            </Card.Section>

            <Group gap='xs' wrap='nowrap' mt='sm' align='flex-start'>
              <ThemeIcon variant='light' size='md' radius='md' style={{ flexShrink: 0 }}>
                <Icon size='1rem' />
              </ThemeIcon>
              <Box style={{ minWidth: 0, flex: 1 }}>
                <Group gap={4} wrap='nowrap'>
                  <Text size='sm' fw={600} truncate='end' title={displayNameOf(file)} style={{ minWidth: 0 }}>
                    {displayNameOf(file)}
                  </Text>
                  {file.password && (
                    <Tooltip label='Password protected' withArrow>
                      <IconLockFilled size='0.85rem' style={{ flexShrink: 0, opacity: 0.6 }} />
                    </Tooltip>
                  )}
                </Group>
                <Text size='xs' c='dimmed'>
                  {bytes(file.size)} · {dayjs(file.createdAt).format('MMM D, YYYY')}
                </Text>
              </Box>
            </Group>

            <Group justify='flex-end' mt='xs'>
              <FileActions file={file} copyLink={copyLink} />
            </Group>
          </Card>
        );
      })}
    </SimpleGrid>
  );
}

// ---------------------------------------------------------------------------
// Page
// ---------------------------------------------------------------------------

export function Component() {
  const { initial } = useLoaderData<typeof loader>();
  const navigate = useNavigate();
  const copy = useCopyUrl();
  const copyFile = (f: PublicFolderFile) => copy(shareUrl(f));
  const copyFolder = (fl: Partial<Folder>) => copy(folderShareUrl(fl));

  const [, setSearchParams] = useSearchParams();
  const [page, setPage] = useQueryState('page', parseAsInteger.withDefault(1));
  const [perpage] = useQueryState('perpage', parseAsInteger.withDefault(15));
  const [sortBy, setSortBy] = useQueryState(
    'sortBy',
    parseAsStringEnum<SortColumn>(['name', 'createdAt', 'updatedAt', 'size']).withDefault('createdAt'),
  );
  const [order, setOrder] = useQueryState(
    'order',
    parseAsStringEnum<SortOrder>(['asc', 'desc']).withDefault('desc'),
  );

  // Persisted list/grid preference.
  const [view, setView] = useState<FolderView>(() => {
    if (typeof window === 'undefined') return 'list';
    return window.localStorage.getItem(VIEW_STORAGE_KEY) === 'grid' ? 'grid' : 'list';
  });
  useEffect(() => {
    try {
      window.localStorage.setItem(VIEW_STORAGE_KEY, view);
    } catch {
      /* localStorage may be unavailable (private mode) — non-fatal */
    }
  }, [view]);

  const { data, isLoading } = useApiPagination<Response['/api/server/folder/[id]']>(
    {
      route: `/api/server/folder/${initial.folder.id}`,
      page,
      perpage,
      sort: sortBy,
      order,
    },
    { fallbackData: initial, keepPreviousData: true, revalidateOnFocus: false },
  );

  const folder = data?.folder ?? initial.folder;
  const files = (data?.page ?? []) as PublicFolderFile[];
  const totalRecords = data?.total ?? 0;
  const cachedPages = data?.pages ?? 0;

  useTitle(folder.name ?? 'Folder');

  const buildBreadcrumbs = () => {
    const items: FolderBreadcrumb[] = [];

    let current = folder.parent as Partial<Folder> | undefined;
    while (current && current.public) {
      items.unshift({ id: current.id!, name: current.name!, public: true });
      current = current.parent as Partial<Folder> | undefined;
    }

    items.push({ id: folder.id!, name: folder.name!, public: true });

    return items;
  };

  const breadcrumbs = buildBreadcrumbs();
  const children = (folder.children ?? []) as Partial<Folder>[];
  const from = totalRecords === 0 ? 0 : (page - 1) * perpage + 1;
  const to = Math.min(page * perpage, totalRecords);

  // Folders are shown above files, sorted, on the first page only (they are not
  // paginated, so they would otherwise repeat on every page).
  const sortedFolders = page === 1 ? sortFolders(children, sortBy, order) : [];
  const hasContent = totalRecords > 0 || children.length > 0;

  // Immediate (public) parent for the "up one level" button.
  const parentCrumb = breadcrumbs.length > 1 ? breadcrumbs[breadcrumbs.length - 2] : null;
  const folderCount = children.length;

  const handleSort = (column: SortColumn) => {
    if (sortBy === column) {
      setOrder(order === 'asc' ? 'desc' : 'asc');
    } else {
      setSortBy(column);
      setOrder('asc');
    }
    setPage(1);
  };

  return (
    <Container size='lg' my='lg'>
      {breadcrumbs.length > 1 && (
        <Breadcrumbs mb='md'>
          {breadcrumbs.map((item, index) => (
            <Anchor
              key={item.id}
              onClick={() => navigate(`/folder/${item.id}`)}
              style={{ cursor: 'pointer' }}
              fw={index === breadcrumbs.length - 1 ? 600 : 400}
            >
              {item.name}
            </Anchor>
          ))}
        </Breadcrumbs>
      )}

      <Group justify='space-between' align='center' wrap='wrap' gap='sm'>
        <Group gap='sm' wrap='nowrap'>
          {parentCrumb && (
            <Tooltip label={`Up to ${parentCrumb.name}`} withArrow>
              <ActionIcon
                variant='default'
                size='xl'
                radius='md'
                onClick={() => navigate(`/folder/${parentCrumb.id}`)}
                aria-label='Up one level'
              >
                <IconArrowUp size='1.3rem' />
              </ActionIcon>
            </Tooltip>
          )}
          <ThemeIcon variant='light' size='xl' radius='md'>
            <IconFolder size='1.6rem' />
          </ThemeIcon>
          <div>
            <Title order={1} fz={{ base: 'h2', sm: 'h1' }}>
              {folder.name}
            </Title>
            <Text size='sm' c='dimmed'>
              {folderCount > 0 ? `${folderCount} ${folderCount === 1 ? 'folder' : 'folders'} · ` : ''}
              {totalRecords} {totalRecords === 1 ? 'file' : 'files'}
            </Text>
          </div>
        </Group>

        <Group gap='sm' wrap='wrap'>
          {folder.allowUploads && (
            <Button
              component={Link}
              to={`/folder/${folder.id}/upload`}
              reloadDocument
              variant='default'
              leftSection={<IconUpload size='1rem' />}
            >
              Upload
            </Button>
          )}

          {totalRecords > 0 && (
            <Button
              component='a'
              href={zipUrl(folder.id!)}
              download
              leftSection={<IconArchive size='1rem' />}
            >
              Download all
            </Button>
          )}
        </Group>
      </Group>

      {hasContent && (
        <>
          <Group justify='flex-end' align='center' mt='xl' mb='sm'>
            <SegmentedControl
              size='sm'
              value={view}
              onChange={(v) => setView(v as FolderView)}
              data={[
                {
                  value: 'list',
                  label: (
                    <Center style={{ gap: 6 }}>
                      <IconList size='1rem' />
                      <Box visibleFrom='xs'>List</Box>
                    </Center>
                  ),
                },
                {
                  value: 'grid',
                  label: (
                    <Center style={{ gap: 6 }}>
                      <IconLayoutGrid size='1rem' />
                      <Box visibleFrom='xs'>Grid</Box>
                    </Center>
                  ),
                },
              ]}
            />
          </Group>

          {view === 'list' ? (
            <FileListView
              files={files}
              folders={sortedFolders}
              sortBy={sortBy}
              order={order}
              onSort={handleSort}
              copyLink={copyFile}
              onOpenFolder={(id) => navigate(`/folder/${id}`)}
              onCopyFolder={copyFolder}
            />
          ) : (
            <FileGridView files={files} folders={sortedFolders} copyLink={copyFile} />
          )}
        </>
      )}

      {children.length === 0 && totalRecords === 0 && (
        <Card withBorder radius='md' mt='xl' py='xl'>
          <Stack align='center' gap='xs'>
            <ThemeIcon variant='light' size={56} radius='xl'>
              <IconFolder size='1.8rem' />
            </ThemeIcon>
            <Text fw={600}>This folder is empty</Text>
            {folder.allowUploads && (
              <Button
                component={Link}
                to={`/folder/${folder.id}/upload`}
                reloadDocument
                variant='light'
                leftSection={<IconUpload size='1rem' />}
                mt='xs'
              >
                Upload files
              </Button>
            )}
          </Stack>
        </Card>
      )}

      {totalRecords > 0 && (
        <Group justify='space-between' align='center' mt='lg'>
          <Text size='sm' c='dimmed'>
            {`${from} – ${to} of ${totalRecords} ${totalRecords === 1 ? 'file' : 'files'}`}
            {folderCount > 0 ? ` · ${folderCount} ${folderCount === 1 ? 'folder' : 'folders'}` : ''}
          </Text>

          <Group gap='sm'>
            <Select
              value={perpage.toString()}
              data={PER_PAGE_OPTIONS.map((val) => ({ value: val.toString(), label: `${val}` }))}
              onChange={(value) => {
                setSearchParams((prev) => {
                  prev.set('perpage', value ?? '15');
                  prev.set('page', '1');
                  return prev;
                });
              }}
              w={80}
              size='xs'
              variant='filled'
              disabled={isLoading}
              aria-label='Files per page'
            />

            <Pagination
              value={page}
              onChange={setPage}
              total={cachedPages}
              size='sm'
              withControls
              withEdges
              disabled={isLoading}
            />
          </Group>
        </Group>
      )}
    </Container>
  );
}

Component.displayName = 'ViewFolderId';
