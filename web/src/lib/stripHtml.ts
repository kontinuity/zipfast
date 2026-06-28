import he from 'he';

export function stripHtml(html: string): string {
  return he.encode(html);
}
