import { SchemaContext } from '@prisma/internals';

// @ts-ignore
declare module '@prisma/migrate/dist/utils/ensureDatabaseExists' {
  export function ensureDatabaseExists(schemaContext: SchemaContext): Promise<boolean>;
}
