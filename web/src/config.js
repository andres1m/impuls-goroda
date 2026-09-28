export function parseRuntimeConfig(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('Invalid runtime configuration');
  }

  const { apiBaseUrl, prototypeMode = false, twoGisApiKey = '' } = value;
  if (typeof prototypeMode !== 'boolean') {
    throw new Error('Invalid prototype mode');
  }
  if (typeof twoGisApiKey !== 'string') {
    throw new Error('Invalid map key');
  }
  if (typeof apiBaseUrl !== 'string') {
    throw new Error('Invalid API address');
  }

  let url;
  try {
    url = new URL(apiBaseUrl);
  } catch {
    throw new Error('Invalid API address');
  }

  const local = ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname);
  if (
    (url.protocol !== 'https:' && !(local && url.protocol === 'http:')) ||
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    url.pathname !== '/'
  ) {
    throw new Error('Invalid API address');
  }

  return { apiBaseUrl: url.origin, prototypeMode, ...(twoGisApiKey ? { twoGisApiKey } : {}) };
}

export async function loadRuntimeConfig(url, fetcher = fetch, signal) {
  const response = await fetcher(url, { cache: 'no-store', signal });
  if (!response.ok) {
    throw new Error('Runtime configuration is unavailable');
  }
  return parseRuntimeConfig(await response.json());
}
