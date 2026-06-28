import { Config } from '../config/validate';
import { onUpload as discordOnUpload, onShorten as discordOnShorten } from './discord';
import { onUpload as httpOnUpload, onShorten as httpOnShorten } from './http';

export async function onUpload(config: Config, args: Parameters<typeof discordOnUpload>[1]) {
  Promise.all([discordOnUpload(config, args), httpOnUpload(config, args)]);

  return;
}

export async function onShorten(config: Config, args: Parameters<typeof discordOnShorten>[1]) {
  Promise.all([discordOnShorten(config, args), httpOnShorten(config, args)]);

  return;
}
