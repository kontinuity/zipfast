import { ZiplineTheme } from '@/lib/theme';
import Root from '../Root';
import ViewFileId from '../pages/view/[id]';
import { Config } from '@/lib/config/validate';

export const createRoutes = (themes?: ZiplineTheme[], defaultTheme?: Config['website']['theme']) => [
  {
    path: '/view',
    Component:
      typeof window === 'undefined' ? undefined : () => <Root themes={themes} defaultTheme={defaultTheme} />,
    children: [
      {
        path: ':id',
        Component: () => <ViewFileId />,
      },
    ],
  },
];
