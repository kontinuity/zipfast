export function formatRootUrl(route: string, src: string) {
  return `${route === '/' ? '' : route}/${encodeURI(src)}`;
}

export function trimUrl(length: number, url: string) {
  return url.length > length ? `${url.slice(0, length)}...` : url;
}
