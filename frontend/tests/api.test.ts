import { afterEach, describe, expect, it, vi } from 'vitest'
import { request } from '../src/api'

afterEach(() => vi.unstubAllGlobals())
describe('API contract', () => {
  it('preserves document IDs beyond JavaScript integer precision', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response('{"code":0,"data":{"doc_id":"2109838273063948288"}}')),
    )
    expect(await request('/api/v1/docs')).toEqual({ doc_id: '2109838273063948288' })
  })
  it('rejects business failures even on HTTP 200', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(Response.json({ code: 1001, msg: '文档不存在' })),
    )
    await expect(request('/api/v1/docs')).rejects.toThrow('文档不存在')
  })
  it('reports non-JSON gateway failures without rendering HTML', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response('<html>Bad gateway</html>', { status: 502 })),
    )
    await expect(request('/api/v1/docs')).rejects.toThrow('502')
  })
  it('does not treat an HTTP failure with code zero as success', async () => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(Response.json({ code: 0 }, { status: 503 })))
    await expect(request('/api/v1/docs')).rejects.toThrow('503')
  })
  it('never automatically retries a failed write', async () => {
    const fetch = vi.fn().mockRejectedValue(new TypeError('Failed to fetch'))
    vi.stubGlobal('fetch', fetch)
    await expect(request('/api/v1/docs', { method: 'POST', body: '{}' })).rejects.toThrow('网络')
    expect(fetch).toHaveBeenCalledTimes(1)
  })
})
