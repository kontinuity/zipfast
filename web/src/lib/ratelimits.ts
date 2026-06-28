export const secondlyRatelimit = (seconds: number) => ({
  config: { rateLimit: { max: 1, timeWindow: `${seconds} seconds`, allowList: [] } },
});
