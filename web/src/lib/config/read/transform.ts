import { bytes } from '@/lib/bytes';
import Logger from '@/lib/logger';
import ms, { StringValue } from 'ms';
import { EnvType } from './env';

export function isObject(value: any) {
  return typeof value === 'object' && value !== null;
}

export function setProperty(obj: any, path: string, value: any) {
  if (!isObject(obj)) return obj;

  const root = obj;
  const dot = path.split('.');

  for (let i = 0; i !== dot.length; ++i) {
    const key = dot[i];

    if (i === dot.length - 1) {
      obj[key] = value;
    } else if (!isObject(obj[key])) {
      obj[key] = typeof dot[i + 1] === 'number' ? [] : {};
    }

    obj = obj[key];
  }

  return root;
}

export function getProperty(obj: any, path: string) {
  if (!isObject(obj)) return undefined;

  const dot = path.split('.');

  for (let i = 0; i !== dot.length; ++i) {
    const key = dot[i];

    if (!isObject(obj) || !(key in obj)) return undefined;

    obj = obj[key];
  }

  return obj;
}

export function parse(this: { logger: Logger }, value: string, type: EnvType) {
  switch (type) {
    case 'string':
      return value;
    case 'string[]':
      return value
        .split(',')
        .filter((s) => s.length !== 0)
        .map((s) => s.trim());
    case 'number':
      return number(value);
    case 'boolean':
      return boolean(value);
    case 'byte':
      return bytes(value);
    case 'ms':
      return ms(value as StringValue);
    case 'json':
      try {
        return JSON.parse(value);
      } catch {
        this.logger.error('Failed to parse JSON object', { value });
        return undefined;
      }
    default:
      return undefined;
  }
}

export function number(value: string) {
  const num = Number(value);
  if (isNaN(num)) return undefined;

  return num;
}

export function boolean(value: string) {
  if (value === 'true') return true;
  if (value === 'false') return false;

  return undefined;
}
