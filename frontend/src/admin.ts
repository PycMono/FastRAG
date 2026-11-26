let token = ''
export function setAdminToken(value: string) {
  token = value.trim()
}
export function hasAdminToken() {
  return !!token
}
export async function adminRequest<T>(
  path: string,
  body?: unknown,
  method?: 'POST' | 'DELETE',
): Promise<T> {
  const response = await fetch(`/api/admin/${path}`, {
    method: method || (body === undefined ? 'GET' : 'PUT'),
    headers: {
      Authorization: `Bearer ${token}`,
      ...(body === undefined ? {} : { 'Content-Type': 'application/json' }),
    },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    cache: 'no-store',
    signal: AbortSignal.timeout(path.endsWith('/generate') ? 120000 : 20000),
  })
  let parsed
  try {
    parsed = await response.json()
  } catch {
    throw new Error(`管理服务响应异常（${response.status}）`)
  }
  if (!response.ok || parsed.code !== 0) throw new Error(parsed.msg || '管理请求失败')
  return parsed.data as T
}
