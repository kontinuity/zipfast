declare module 'asciinema-player' {
  export function create(
    src: string,
    container: HTMLElement,
    options?: { autoplay?: boolean; cols?: number; rows?: number },
  ): void;
}
