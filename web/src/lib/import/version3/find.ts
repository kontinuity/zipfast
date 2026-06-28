import { Export3 } from './validateExport';

export function findUser(export3: Export3, id: string | undefined | null) {
  if (!id) return null;

  return export3.users[id];
}

export function findFilesByUser(export3: Export3, id: string) {
  return Object.entries(export3.files)
    .filter(([_, file]) => file.user === id)
    .map(([_id, file]) => file);
}
