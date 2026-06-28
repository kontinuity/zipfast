import { Alert, Button, Flex, Text } from '@mantine/core';
import { IconEyeFilled } from '@tabler/icons-react';
import { useState } from 'react';
import KaTeX from './KaTeX';
import Markdown from './Markdown';
import HighlightCode from './code/HighlightCode';
import { RenderMode } from './renderMode';

export function RenderAlert({
  renderer,
  state,
  change,
}: {
  renderer: string;
  state: boolean;
  change: (s: boolean) => void;
}) {
  return (
    <Alert
      icon={<IconEyeFilled size='1rem' />}
      variant='outline'
      mb='sm'
      styles={{ message: { marginTop: 0 } }}
    >
      <Flex align='center' justify='space-between' wrap='wrap' gap='md'>
        <Text style={{ flex: 1, minWidth: '200px' }}>
          {!state
            ? `This file is rendered through ${renderer}`
            : `This file can be rendered through ${renderer}`}
        </Text>

        <Button size='compact-sm' onClick={() => change(!state)} w={{ base: '100%', xs: 'auto' }}>
          {state ? 'Show' : 'Hide'} rendered version
        </Button>
      </Flex>
    </Alert>
  );
}

export default function Render({
  mode,
  language,
  code,
  ...props
}: {
  mode: RenderMode;
  language: string;
  code: string;

  [key: string]: any;
}) {
  const [highlight, setHighlight] = useState(false);

  switch (mode) {
    case RenderMode.Katex:
      return (
        <>
          <RenderAlert renderer='KaTeX' state={highlight} change={(s) => setHighlight(s)} />

          {highlight ? <HighlightCode language={language} code={code} {...props} /> : <KaTeX tex={code} />}
        </>
      );
    case RenderMode.Markdown:
      return (
        <>
          <RenderAlert renderer='Markdown' state={highlight} change={(s) => setHighlight(s)} />

          {highlight ? <HighlightCode language={language} code={code} {...props} /> : <Markdown md={code} />}
        </>
      );
    default:
      return <HighlightCode language={language} code={code} {...props} />;
  }
}
