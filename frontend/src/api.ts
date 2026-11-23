export async function request<T>(path: string, init?: RequestInit): Promise<T> {
  let response: Response
  try {
    response = await fetch(path, init)
  } catch {
    throw new Error(
      '网络连接失败，请检查服务连接。写入结果可能尚未确认，请先刷新文档列表再决定是否重试。',
    )
  }
  let envelope: { code: number; msg?: string; data: T }
  try {
    envelope = await response.json()
  } catch {
    throw new Error(`服务返回异常（HTTP ${response.status}），请稍后重试。`)
  }
  if (!response.ok) throw new Error(envelope?.msg || `服务暂不可用（HTTP ${response.status}）`)
  if (!envelope || envelope.code !== 0)
    throw new Error(envelope?.msg || '请求未完成，请检查请求参数。')
  return envelope.data
}
export function get<T>(path: string, params: Record<string, string | number>) {
  return request<T>(
    `${path}?${new URLSearchParams(Object.entries(params).map(([key, value]) => [key, String(value)]))}`,
  )
}
export function post<T>(path: string, body: unknown) {
  return request<T>(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
}
