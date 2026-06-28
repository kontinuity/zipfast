import { Paper, Typography } from '@mantine/core';
import Marked from 'marked-react';
import HighlightCode from './code/HighlightCode';
import { sanitize } from 'isomorphic-dompurify';

const components = {
  code(value: string, language?: string) {
    return <HighlightCode code={value} language={language ?? 'text'} />;
  },
};

export default function Markdown({ md }: { md: string }) {
  const cleanedMd = sanitize(md, {
    ALLOWED_TAGS: ['b', 'i', 'em', 'strong', 'a', 'p', 'br', 'code', 'pre', 'span'],
    ALLOWED_ATTR: ['href', 'title', 'class'],
  });

  return (
    <Paper withBorder p='md'>
      <Typography>
        <Marked value={cleanedMd} gfm renderer={components} />
      </Typography>
    </Paper>
  );
}
