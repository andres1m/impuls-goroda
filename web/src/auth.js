export async function exchangeMaxInitData(apiBaseUrl, initData, fetcher = fetch, signal) {
  if (typeof initData !== 'string' || initData.length === 0) {
    throw new Error('MAX launch data is unavailable');
  }

  const response = await fetcher(`${apiBaseUrl}/api/v1/auth/max`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ init_data: initData }),
    credentials: 'omit',
    cache: 'no-store',
    signal,
  });
  if (!response.ok) {
    throw new Error('MAX authorization failed');
  }

  const body = await response.json();
  if (
    !body ||
    typeof body.access_token !== 'string' ||
    body.access_token.length === 0 ||
    body.token_type !== 'Bearer' ||
    typeof body.expires_at !== 'string'
  ) {
    throw new Error('Invalid authorization response');
  }
  return { accessToken: body.access_token, expiresAt: body.expires_at };
}
