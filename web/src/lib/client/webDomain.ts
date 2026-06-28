import { useSettingsStore } from './store/settings';

export function getDomain(path: string): string {
  const {
    settings: { domain },
  } = useSettingsStore.getState();

  const { protocol, host } = window.location;

  if ((domain ?? '').trim() === '') {
    return `${protocol}//${host}${path}`;
  } else {
    return `${protocol}//${domain}${path}`;
  }
}
