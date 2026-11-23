import { expect, test, type Page } from '@playwright/test'

const id = '2109838273063948288'
const doc = {
  doc_id: id,
  doc_name: '产品手册.md',
  title: '产品手册',
  chunk_count: 3,
  has_content: true,
  create_ts: 1794900000000,
  update_ts: 1794900000000,
}
async function fixture(page: Page) {
  const requests: { path: string; body: any; params: URLSearchParams }[] = []
  let deleted = false
  await page.route('**/ready', (route) =>
    route.fulfill({ json: { status: 'ready', checks: { mysql: 'ok' } } }),
  )
  await page.route('**/api/v1/**', async (route) => {
    const request = route.request(),
      url = new URL(request.url()),
      body = request.postDataJSON()
    requests.push({ path: url.pathname, body, params: url.searchParams })
    let data: unknown
    if (url.pathname === '/api/v1/docs' && request.method() === 'GET') {
      const second = url.searchParams.get('page') === '2'
      const other = url.searchParams.get('tenant') === 'other'
      data = {
        list: deleted
          ? []
          : [{ ...doc, title: other ? '另一租户的文档' : second ? '第二页的文档' : '产品手册' }],
        total: deleted ? 0 : 11,
        pages: deleted ? 1 : 2,
        page: second ? 2 : 1,
      }
    } else if (url.pathname === '/api/v1/docs/detail') {
      data = {
        ...doc,
        content: '# 产品手册\n\n<script>window.injected = true</script>\n文档正文',
        source_url: 'javascript:alert(1)',
      }
    } else if (url.pathname === '/api/v1/docs/chunks')
      data = {
        items: [
          {
            chunk_id: 'chunk-1',
            order: 0,
            title: '开始使用',
            content: '切片正文内容',
            heading_path: '产品手册 / 开始使用',
          },
        ],
        total: 1,
        offset: 0,
        limit: 20,
      }
    else if (url.pathname === '/api/v1/docs/delete') {
      deleted = true
      data = { doc_name: doc.doc_name, deleted_chunks: 3 }
    } else if (url.pathname === '/api/v1/search')
      data = {
        items: Array.from({ length: 12 }, (_, n) => ({
          ...doc,
          chunk_id: `chunk-${n}`,
          content: `检索结果正文 ${n}`,
          score: 0.92 - n * 0.01,
          heading_path: '产品手册 / 快速开始',
        })),
      }
    else data = { ...doc, created: true }
    await route.fulfill({ json: { code: 0, data } })
  })
  return requests
}

test('document pagination, exact ID, safe detail, chunks and confirmed deletion', async ({
  page,
}) => {
  const requests = await fixture(page)
  await page.goto('/#documents')
  await expect(page.getByRole('heading', { name: '文档管理', exact: true })).toBeVisible()
  await expect(page.getByRole('button', { name: '产品手册 产品手册.md' })).toBeVisible()
  await page.screenshot({ path: '/tmp/fastrag-workbench-documents.png', fullPage: true })
  await page.getByRole('button', { name: '下一页', exact: true }).first().click()
  await expect(page.getByRole('button', { name: '第二页的文档 产品手册.md' })).toBeVisible()
  await page.getByRole('button', { name: '查看', exact: true }).click()
  const dialog = page.getByRole('dialog')
  await expect(dialog.getByText(`文档编号 ${id}`)).toBeVisible()
  await expect(dialog.getByRole('heading', { name: '产品手册', exact: true }).last()).toBeVisible()
  await expect(dialog.locator('.markdown-content script')).toHaveCount(0)
  await dialog.getByRole('button', { name: '原始文本' }).click()
  await expect(
    dialog.getByText('<script>window.injected = true</script>', { exact: false }),
  ).toBeVisible()
  await expect(dialog.getByRole('link', { name: '查看来源' })).toHaveCount(0)
  expect(requests.find((r) => r.path.endsWith('/detail'))?.params.get('doc_id')).toBe(id)
  await dialog.getByRole('button', { name: '检索切片', exact: true }).click()
  await expect(dialog.getByText('切片正文内容')).toBeVisible()
  await page.keyboard.press('Escape')
  await expect(dialog).toHaveCount(0)
  await page.getByRole('button', { name: '删除 产品手册.md' }).click()
  expect(requests.filter((r) => r.path.endsWith('/delete'))).toHaveLength(0)
  await page.getByRole('button', { name: '确认删除' }).click()
  await expect(page.getByText('知识库中还没有文档')).toBeVisible()
  expect(requests.find((r) => r.path.endsWith('/delete'))?.body).toEqual({
    tenant: 'demo',
    kb_no: 'demo-kb',
    doc_name: '产品手册.md',
  })
})

test('file import, pasted text and URL use existing API contracts', async ({ page }) => {
  const requests = await fixture(page)
  await page.goto('/#import')
  await page.getByLabel('选择文档文件').setInputFiles({
    name: '知识手册.md',
    mimeType: 'text/markdown',
    buffer: Buffer.from('# 手册\n测试正文'),
  })
  await page.getByRole('button', { name: '导入到知识库' }).click()
  await expect(page.getByText('文档导入成功')).toBeVisible()
  await page.screenshot({ path: '/tmp/fastrag-workbench-import.png', fullPage: true })
  const fileBody = requests.find((r) => r.path === '/api/v1/docs' && r.body)?.body
  expect(fileBody).toMatchObject({
    tenant: 'demo',
    kb_no: 'demo-kb',
    doc_name: '知识手册.md',
    format: 'markdown',
    content: '# 手册\n测试正文',
    refresh: true,
  })
  await page.getByRole('button', { name: '粘贴文本', exact: true }).click()
  await page.getByLabel('文档名称').fill('粘贴文档.md')
  await page.getByLabel('文档正文').fill('粘贴的正文')
  await page.getByRole('button', { name: '导入到知识库' }).click()
  await expect(page.getByText('文档导入成功')).toBeVisible()
  await page.getByRole('button', { name: '链接导入', exact: true }).click()
  await page.getByLabel('文档链接').fill('https://example.com/manual.pdf')
  await page.getByRole('button', { name: '解析并导入' }).click()
  await expect(page.getByText('文档导入成功')).toBeVisible()
  expect(requests.find((r) => r.path.endsWith('/parse'))?.body).toEqual({
    tenant: 'demo',
    kb_no: 'demo-kb',
    url: 'https://example.com/manual.pdf',
    refresh: true,
  })
})

test('search pagination and persisted workspace isolation', async ({ page }) => {
  const requests = await fixture(page)
  await page.goto('/#search')
  await page.getByLabel('你想在知识库中查找什么？').fill('怎么使用？')
  await page.getByRole('button', { name: '开始检索' }).click()
  await expect(page.getByText('检索结果正文 0', { exact: true })).toBeVisible()
  await page.screenshot({ path: '/tmp/fastrag-workbench-search.png', fullPage: true })
  await page.getByRole('button', { name: '下一页', exact: true }).last().click()
  await expect(page.getByText('检索结果正文 10', { exact: true })).toBeVisible()
  expect(requests.find((r) => r.path.endsWith('/search'))?.body).toMatchObject({
    tenant: 'demo',
    kb_nos: ['demo-kb'],
    query: '怎么使用？',
    rerank_switch: true,
  })
  await page.getByRole('button', { name: '切换工作区' }).click()
  await page.getByLabel('租户标识').fill('other')
  await page.getByLabel('知识库编号').fill('new-kb')
  await page.getByRole('button', { name: '应用工作区' }).click()
  await expect(page.getByRole('heading', { name: '从一个问题开始' })).toBeVisible()
  await page.getByRole('link', { name: '文档管理', exact: true }).click()
  await expect(page.getByRole('button', { name: '另一租户的文档 产品手册.md' })).toBeVisible()
  await page.reload()
  await expect(page.getByRole('button', { name: '另一租户的文档 产品手册.md' })).toBeVisible()
})

test('API failure has retry and import failure keeps editable content', async ({ page }) => {
  await fixture(page)
  await page.route('**/api/v1/docs?**', (route) =>
    route.fulfill({ json: { code: 1001, msg: '文档服务暂不可用' } }),
  )
  await page.goto('/#documents')
  await expect(page.getByRole('alert')).toContainText('文档服务暂不可用')
  await expect(page.getByRole('button', { name: '重新加载' })).toBeVisible()
  await page.getByRole('link', { name: '导入文档', exact: true }).click()
  await page.getByRole('button', { name: '粘贴文本' }).click()
  await page.getByLabel('文档名称').fill('重试.md')
  await page.getByLabel('文档正文').fill('保留的正文')
  await page.route('**/api/v1/docs', (route) =>
    route.fulfill({ json: { code: 1002, msg: '模型不可用' } }),
  )
  await page.getByRole('button', { name: '导入到知识库' }).click()
  await expect(page.getByRole('alert')).toContainText('模型不可用')
  await expect(page.getByLabel('文档正文')).toHaveValue('保留的正文')
  await expect(page.getByRole('button', { name: '导入到知识库' })).toBeEnabled()
})

test('mobile navigation and layout fit the viewport', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await fixture(page)
  await page.goto('/#documents')
  await expect(page.getByRole('heading', { name: '文档管理', exact: true })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.getByRole('link', { name: '导入文档', exact: true }).click()
  await expect(page.getByRole('button', { name: '上传文件', exact: true })).toBeVisible()
  await page.screenshot({ path: '/tmp/fastrag-workbench-mobile.png', fullPage: true })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.getByRole('link', { name: '检索测试', exact: true }).click()
  await expect(page.getByRole('button', { name: '开始检索' })).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
})

test('late responses from the previous workspace cannot repopulate the new view', async ({
  page,
}) => {
  await fixture(page)
  let release: () => void = () => {}
  const held = new Promise<void>((resolve) => {
    release = resolve
  })
  let started: () => void = () => {}
  const arrived = new Promise<void>((resolve) => {
    started = resolve
  })
  await page.route('**/api/v1/search', async (route) => {
    started()
    await held
    await route.fulfill({
      json: {
        code: 0,
        data: { items: [{ ...doc, chunk_id: 'old', content: '旧租户的延迟结果', score: 0.9 }] },
      },
    })
  })
  await page.goto('/#search')
  await page.getByLabel('你想在知识库中查找什么？').fill('旧问题')
  await page.getByRole('button', { name: '开始检索' }).click()
  await arrived
  await page.getByRole('button', { name: '切换工作区' }).click()
  await page.getByLabel('租户标识').fill('other')
  await page.getByRole('button', { name: '应用工作区' }).click()
  release()
  await expect(page.getByRole('heading', { name: '从一个问题开始' })).toBeVisible()
  await expect(page.getByText('旧租户的延迟结果')).toHaveCount(0)
})

test('query links auto-search and malformed storage does not break startup', async ({ page }) => {
  const requests = await fixture(page)
  await page.addInitScript(() => localStorage.setItem('fastrag.workspace', '{broken-json'))
  await page.goto('/?q=配置模型')
  await expect(page.getByText('检索结果正文 0', { exact: true })).toBeVisible()
  expect(requests.find((r) => r.path.endsWith('/search'))?.body.query).toBe('配置模型')
  await expect(page.getByRole('heading', { name: '检索测试', exact: true })).toBeVisible()
})

test('keyboard skip link focuses current content without changing the page', async ({ page }) => {
  await fixture(page)
  await page.goto('/#search')
  const skip = page.getByRole('link', { name: '跳转到主要内容' })
  await skip.focus()
  await page.keyboard.press('Enter')
  await expect(page).toHaveURL(/#search$/)
  await expect(page.locator('main')).toBeFocused()
  await expect(page.getByRole('heading', { name: '检索测试', exact: true })).toBeVisible()
})

test('import drafts survive switching methods and successful import links to search', async ({
  page,
}) => {
  await fixture(page)
  await page.goto('/#import')
  await page.getByRole('button', { name: '粘贴文本', exact: true }).click()
  await page.getByLabel('文档名称').fill('草稿.md')
  await page.getByLabel('文档正文').fill('保留草稿正文')
  await page.getByRole('button', { name: '链接导入', exact: true }).click()
  await page.getByLabel('文档链接').fill('https://example.com/test.pdf')
  await page.getByRole('button', { name: '粘贴文本', exact: true }).click()
  await expect(page.getByLabel('文档名称')).toHaveValue('草稿.md')
  await expect(page.getByLabel('文档正文')).toHaveValue('保留草稿正文')
  await page.getByRole('button', { name: '导入到知识库' }).click()
  await page.getByRole('button', { name: '去检索验证' }).click()
  await expect(page).toHaveURL(/#search$/)
})

test('batch import reports partial failures and never retries successful files', async ({
  page,
}) => {
  await fixture(page)
  const batches: any[] = []
  await page.route('**/api/v1/docs/batch', async (route) => {
    batches.push(route.request().postDataJSON())
    await route.fulfill({
      json: {
        code: 0,
        data: {
          total: 2,
          success: 1,
          failed: 1,
          items: [{ ...doc, doc_name: '成功.md', created: true }],
          errors: [{ doc_name: '失败.md', message: '模型暂不可用' }],
        },
      },
    })
  })
  await page.goto('/#import')
  await page.getByRole('button', { name: '批量上传', exact: true }).click()
  await page.getByLabel('选择批量文档').setInputFiles([
    { name: '成功.md', mimeType: 'text/markdown', buffer: Buffer.from('# 成功') },
    { name: '失败.md', mimeType: 'text/markdown', buffer: Buffer.from('# 失败') },
  ])
  await page.getByRole('button', { name: '导入 2 份文档', exact: true }).click()
  await expect(page.getByText('模型暂不可用')).toBeVisible()
  await expect(page.getByText('· 已成功导入 3 个切片')).toBeVisible()
  await expect(page.getByRole('button', { name: '导入 0 份文档', exact: true })).toBeDisabled()
  expect(batches).toHaveLength(1)
  expect(batches[0][0]).toMatchObject({ doc_name: '成功.md', content: '# 成功', tenant: 'demo' })
})

test('current page filters are explicit and page size reaches the server', async ({ page }) => {
  const requests = await fixture(page)
  await page.goto('/#documents')
  await expect(page.getByRole('button', { name: '产品手册 产品手册.md' })).toBeVisible()
  await page.getByLabel('筛选当前页文档').fill('不存在')
  await expect(page.getByText('当前页没有匹配的文档')).toBeVisible()
  await expect(page.getByText('其他分页不参与筛选。', { exact: false })).toBeVisible()
  await page.getByRole('button', { name: '清除筛选', exact: true }).last().click()
  await expect(page.getByRole('button', { name: '产品手册 产品手册.md' })).toBeVisible()
  await page.getByLabel('每页文档数').selectOption('20')
  await expect.poll(() => requests.filter((r) => r.params.get('page_size') === '20').length).toBe(1)
})

test('query presets and history remain scoped to the selected workspace', async ({ page }) => {
  const requests = await fixture(page)
  await page.goto('/#search')
  await page.getByRole('button', { name: '关键词优先', exact: true }).click()
  await page.getByLabel('你想在知识库中查找什么？').fill('历史问题')
  await page.getByRole('button', { name: '开始检索' }).click()
  await expect(page.getByRole('button', { name: '历史问题', exact: true })).toBeVisible()
  expect(requests.find((r) => r.path.endsWith('/search'))?.body.dense_weight).toBe(0)
  await page.reload()
  await expect(page.getByRole('button', { name: '历史问题', exact: true })).toBeVisible()
  await page.getByRole('button', { name: '切换工作区' }).click()
  await page.getByLabel('租户标识').fill('other')
  await page.getByRole('button', { name: '应用工作区' }).click()
  await expect(page.getByRole('button', { name: '历史问题', exact: true })).toHaveCount(0)
})

test('in-flight imports protect workspace switching and release it on completion', async ({
  page,
}) => {
  await fixture(page)
  let release: () => void = () => {}
  const held = new Promise<void>((resolve) => {
    release = resolve
  })
  await page.route('**/api/v1/docs', async (route) => {
    await held
    await route.fulfill({ json: { code: 0, data: { ...doc, created: true } } })
  })
  await page.goto('/#import')
  await page.getByRole('button', { name: '粘贴文本', exact: true }).click()
  await page.getByLabel('文档名称').fill('处理中.md')
  await page.getByLabel('文档正文').fill('正文')
  await page.getByRole('button', { name: '导入到知识库' }).click()
  await expect(page.getByRole('button', { name: '切换工作区' })).toBeDisabled()
  await page.getByRole('link', { name: '检索测试', exact: true }).click()
  await expect(page.getByRole('button', { name: '切换工作区' })).toBeDisabled()
  release()
  await expect(page.getByRole('button', { name: '切换工作区' })).toBeEnabled()
})

test('dependency status shows actionable detail', async ({ page }) => {
  await fixture(page)
  await page.goto('/#documents')
  await page.getByRole('button', { name: '服务已就绪', exact: true }).click()
  await expect(page.getByText('MySQL 数据库')).toBeVisible()
  await expect(page.getByText('运行正常')).toBeVisible()
})

test('mobile wrapped Chinese snippets always have an expand action', async ({ page }) => {
  await page.setViewportSize({ width: 390, height: 844 })
  await fixture(page)
  const content = '这是需要完整阅读的中文片段。'.repeat(14)
  await page.route('**/api/v1/search', (route) =>
    route.fulfill({
      json: { code: 0, data: { items: [{ ...doc, chunk_id: 'wrapped', content, score: 0.9 }] } },
    }),
  )
  await page.goto('/#search')
  await page.getByLabel('你想在知识库中查找什么？').fill('中文')
  await page.getByRole('button', { name: '开始检索' }).click()
  await page.getByRole('button', { name: '展开完整片段' }).click()
  await expect(page.locator('.result-content')).not.toHaveClass(/collapsed/)
  await expect(page.getByRole('button', { name: '收起片段' })).toBeVisible()
})

test('split search reader follows selected result and opens its document', async ({ page }) => {
  const requests = await fixture(page)
  await page.goto('/#search')
  await page.getByLabel('你想在知识库中查找什么？').fill('使用说明')
  await page.getByRole('button', { name: '开始检索' }).click()
  await page.locator('.match-item').nth(1).click()
  await expect(page.locator('.result-reader .result-content')).toHaveText('检索结果正文 1')
  await page.getByRole('button', { name: '查看原文', exact: true }).click()
  await expect(page.getByRole('dialog')).toBeVisible()
  expect(requests.find((r) => r.path.endsWith('/detail'))?.params.get('doc_id')).toBe(id)
})

test('visual review with a populated library and readable document', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 960 })
  await fixture(page)
  const names = [
    '产品使用手册.md',
    'API 接入与认证指南.md',
    '知识库部署说明.md',
    '常见问题与排查.md',
    '客户服务规范.txt',
    '版本更新记录.md',
    '数据导入操作指南.md',
  ]
  await page.route('**/api/v1/docs?**', (route) =>
    route.fulfill({
      json: {
        code: 0,
        data: {
          list: names.map((name, i) => ({
            ...doc,
            doc_id: `${id}${i}`,
            doc_name: name,
            title: name.replace(/\.(md|txt)$/, ''),
            chunk_count: [24, 18, 32, 12, 8, 16, 10][i],
          })),
          total: 7,
          pages: 1,
          page: 1,
        },
      },
    }),
  )
  await page.route('**/api/v1/docs/detail?**', (route) =>
    route.fulfill({
      json: {
        code: 0,
        data: {
          ...doc,
          title: '产品使用手册',
          content:
            '# 产品使用手册\n\n欢迎使用 FastRAG。这份手册将帮助你完成从文档导入到检索验证的完整流程。\n\n## 开始之前\n\n请确认已选择正确的租户和知识库。所有文档与检索操作都在当前工作区内完成。\n\n## 导入你的第一份文档\n\n1. 在左侧选择 **导入文档**。\n2. 上传 Markdown 或 TXT 文件，也可以直接粘贴正文。\n3. 确认文档名称后，点击导入。\n\n> 同名文档会覆盖更新。建议使用清晰、稳定的文档名称。\n\n## 验证检索效果\n\n使用真实问题进行检索，查看匹配片段，并通过原文确认内容来源。',
        },
      },
    }),
  )
  await page.goto('/#documents')
  await expect(page.getByRole('button', { name: '产品使用手册 产品使用手册.md' })).toBeVisible()
  await page.screenshot({ path: '/tmp/fastrag-redesign-library.png', fullPage: true })
  await page.getByRole('button', { name: '查看', exact: true }).first().click()
  await expect(page.getByRole('heading', { name: '开始之前' })).toBeVisible()
  await page.screenshot({ path: '/tmp/fastrag-redesign-reader.png', fullPage: true })
  await page.keyboard.press('Escape')
  await page.getByRole('link', { name: '导入文档', exact: true }).click()
  await page.screenshot({ path: '/tmp/fastrag-redesign-import.png', fullPage: true })
})

test('document grid and list share filters and document navigation', async ({ page }) => {
  await fixture(page)
  await page.goto('/#documents')
  await page.getByRole('button', { name: '网格视图' }).click()
  await expect(page.locator('.document-grid')).toBeVisible()
  await expect(page.getByRole('button', { name: '网格视图' })).toHaveAttribute(
    'aria-pressed',
    'true',
  )
  await page.getByLabel('筛选当前页文档').fill('不存在')
  await expect(page.getByText('当前页没有匹配的文档')).toBeVisible()
  await page.getByRole('button', { name: '清除筛选', exact: true }).last().click()
  await page.locator('.tile-open').first().click()
  await expect(page.getByRole('dialog')).toBeVisible()
  await page.keyboard.press('Escape')
  await page.getByRole('button', { name: '列表视图' }).click()
  await expect(page.getByRole('table')).toBeVisible()
})

test('homepage presents the product and routes directly to tools', async ({ page }) => {
  await fixture(page)
  await page.goto('/')
  await expect(page.getByRole('heading', { name: /知识就位。.*答案，更进一步。/ })).toBeVisible()
  await page.screenshot({ path: '/tmp/fastrag-editorial-home.png', fullPage: true })
  await page.getByRole('button', { name: '导入你的文档' }).click()
  await expect(page.getByRole('button', { name: '上传文件', exact: true })).toBeVisible()
  await page.setViewportSize({ width: 390, height: 844 })
  await page.goto('/')
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({ path: '/tmp/fastrag-editorial-mobile.png', fullPage: true })
})

test('config management authenticates, edits, adds, deletes and saves a versioned draft', async ({
  page,
}) => {
  await fixture(page)
  let saved: any
  await page.route('**/api/admin/**', async (route) => {
    const req = route.request()
    const path = new URL(req.url()).pathname
    if (req.headers().authorization !== 'Bearer test-token-for-browser-session')
      return route.fulfill({ status: 401, json: { code: 401, msg: '管理令牌不正确' } })
    let data: any = { uptime_seconds: 90 }
    if (path.endsWith('/config')) {
      if (req.method() === 'PUT') saved = req.postDataJSON()
      data = {
        version: saved ? 'v2' : 'v1',
        requires_restart: true,
        config: saved?.config || {
          app: 'fastrag',
          embedding: {
            default: 'local',
            dim: 1024,
            models: {
              local: {
                protocol: 'openai',
                url: 'http://localhost:11434/api/embeddings',
                model: 'bge',
                api_key: '••••••••',
              },
            },
          },
          mysql: { host: 'localhost', password: '••••••••' },
        },
      }
    }
    return route.fulfill({ json: { code: 0, data } })
  })
  await page.goto('/#config')
  await page.getByLabel('管理令牌').fill('test-token-for-browser-session')
  await page.getByRole('button', { name: '验证并继续' }).click()
  await expect(page.getByRole('button', { name: '新增模型' })).toBeVisible()
  await page.getByRole('button', { name: '新增模型' }).click()
  await page.getByLabel('模型配置名称').fill('new-model')
  await page.getByRole('button', { name: '添加到配置' }).click()
  await expect(page.getByRole('button', { name: '删除模型 new-model' })).toBeVisible()
  await page.getByRole('button', { name: '删除模型 new-model' }).click()
  await page.getByRole('button', { name: '确认删除配置' }).click()
  await page.getByLabel('向量维度').fill('768')
  await page.getByRole('button', { name: '保存配置', exact: true }).click()
  await expect(page.getByText('当前服务仍使用启动时的配置', { exact: false })).toBeVisible()
  expect(saved.version).toBe('v1')
  expect(saved.config.embedding.dim).toBe(768)
  expect(saved.config.mysql.password).toBe('••••••••')
  await page.getByRole('button', { name: '完整 JSON', exact: true }).click()
  const jsonEditor = page.getByLabel('完整 JSON 配置')
  const changedJSON = (await jsonEditor.inputValue()).replace('768', '512')
  await jsonEditor.fill(changedJSON)
  await page.getByRole('button', { name: '完整 JSON', exact: true }).click()
  await expect(jsonEditor).toHaveValue(changedJSON)
  await page.getByRole('button', { name: '保存配置', exact: true }).click()
  await expect(page.getByRole('button', { name: '保存配置', exact: true })).toBeDisabled()
  expect(saved.config.embedding.dim).toBe(512)
  await page.screenshot({ path: '/tmp/fastrag-editorial-config.png', fullPage: true })
  await page.getByRole('button', { name: '锁定管理页面' }).click()
  await expect(page.getByLabel('管理令牌')).toBeVisible()
})

test('logs management searches application events and displays errors', async ({ page }) => {
  await fixture(page)
  const queries: string[] = []
  await page.route('**/api/admin/**', async (route) => {
    const url = new URL(route.request().url())
    let data: any = {
      uptime_seconds: 3670,
      log_scope: '结构化应用日志',
      log_disk_retention: '保留最近 2000 条',
    }
    if (url.pathname.endsWith('/logs')) {
      queries.push(url.search)
      data = {
        items: [
          {
            id: 1,
            time: '2026-11-23T10:20:00Z',
            level: 'error',
            message: '向量服务连接失败',
            fields: { path: '/api/v1/docs' },
          },
        ],
        total: 1,
        page: 1,
        limit: 50,
        retained: 1,
        counts: { error: 1, info: 0, warn: 0 },
      }
    }
    await route.fulfill({ json: { code: 0, data } })
  })
  await page.goto('/#logs')
  await page.getByLabel('管理令牌').fill('test-token-for-browser-session')
  await page.getByRole('button', { name: '验证并继续' }).click()
  await expect(page.getByText('向量服务连接失败')).toBeVisible()
  await page.getByLabel('日志级别').selectOption('error')
  await page.getByLabel('搜索日志').fill('连接失败')
  await page.getByRole('button', { name: '搜索', exact: true }).click()
  await expect
    .poll(() => queries.some((q) => q.includes('level=error') && q.includes('q=')))
    .toBe(true)
  await page.getByText('向量服务连接失败').click()
  await expect(page.getByText('"path": "/api/v1/docs"', { exact: false })).toBeVisible()
  await page.screenshot({ path: '/tmp/fastrag-editorial-logs.png', fullPage: true })
})

test('retrieval weight slider, numeric input and presets stay in sync with the API', async ({
  page,
}) => {
  const requests = await fixture(page)
  await page.goto('/#search')
  const slider = page.getByRole('slider', { name: '语义检索权重' })
  const weight = page.getByRole('spinbutton', { name: '语义权重百分比' })
  await expect(slider).toHaveValue('50')
  await slider.focus()
  await slider.press('End')
  await slider.press('ArrowLeft')
  await expect(weight).toHaveValue('99')
  await weight.fill('70')
  await expect(slider).toHaveValue('70')
  await expect(page.getByText('关键词 30%', { exact: true })).toBeVisible()
  await expect(page.getByText('语义 70%', { exact: true })).toBeVisible()
  await expect(page.getByText('自定义权重', { exact: true })).toBeVisible()
  await page.getByLabel('你想在知识库中查找什么？').fill('供需关系')
  await page.getByRole('button', { name: '开始检索' }).click()
  await expect.poll(() => requests.filter((r) => r.path.endsWith('/search')).length).toBe(1)
  expect(requests.find((r) => r.path.endsWith('/search'))?.body.dense_weight).toBe(0.7)
  await page.getByRole('button', { name: '关键词优先', exact: true }).click()
  await expect(slider).toHaveValue('0')
  await page.getByRole('button', { name: '语义优先', exact: true }).click()
  await expect(slider).toHaveValue('100')
  await page.getByRole('button', { name: '均衡检索', exact: true }).click()
  await expect(weight).toHaveValue('50')
  await page.setViewportSize({ width: 390, height: 844 })
  await expect(slider).toBeVisible()
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
  await page.screenshot({ path: '/tmp/fastrag-search-tuning-mobile.png', fullPage: true })
})

test('search diagnostics explain score provenance and surface rerank degradation', async ({
  page,
}) => {
  await fixture(page)
  await page.route('**/api/v1/search', async (route) => {
    const body = route.request().postDataJSON()
    expect(body.diagnostics).toBe(true)
    expect(body.fusion_min_score).toBe(0)
    await route.fulfill({
      json: {
        code: 0,
        data: {
          items: [
            {
              ...doc,
              chunk_id: 'explain-1',
              content: '供需关系的定义',
              heading_path: '经济学 / 供需',
              score: 0.015,
              diagnostics: {
                bm25_rank: 2,
                bm25_score: 9.2,
                knn_rank: 1,
                knn_score: 1.7,
                fusion_score: 0.015,
              },
            },
          ],
          meta: {
            score_type: 'rrf',
            rerank_requested: true,
            rerank_applied: false,
            degraded: true,
            threshold_skipped: true,
            warnings: [
              '精排失败，已使用融合排序。',
              '未取得精排分数的结果跳过精排阈值；可单独设置融合分阈值。',
            ],
          },
          diagnostics: {
            bm25_top: 80,
            knn_top: 100,
            num_candidates: 500,
            rerank_budget: 40,
            dense_weight: 0.5,
            rank_constant: 60,
            counts: {
              bm25: 70,
              knn: 100,
              fused: 130,
              alive: 128,
              after_fusion_threshold: 128,
              rerank_candidates: 40,
              returned: 1,
            },
            timings_ms: {
              embedding: 100,
              retrieval: 30,
              bm25: 20,
              knn: 25,
              fusion: 1,
              alive_filter: 2,
              rerank: 400,
              total: 540,
            },
          },
        },
      },
    })
  })
  await page.goto('/#search')
  await page.getByLabel('你想在知识库中查找什么？').fill('供需关系')
  await page.getByRole('button', { name: '开始检索' }).click()
  await expect(page.getByText('精排失败，已使用融合排序。', { exact: true })).toBeVisible()
  await page.getByText('检索诊断', { exact: true }).click()
  await expect(page.getByText('本次生效参数', { exact: true })).toBeVisible()
  await expect(page.getByText('当前片段的排序来源', { exact: true })).toBeVisible()
  await expect(page.getByText('原始分 9.2000', { exact: true })).toBeVisible()
  await expect(page.getByText('540.0 ms', { exact: true })).toBeVisible()
  await expect(page.getByText('未执行', { exact: true })).toBeVisible()
  await page.screenshot({ path: '/tmp/fastrag-search-diagnostics.png', fullPage: true })
  await page.setViewportSize({ width: 390, height: 844 })
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true)
})

test('rerank threshold is not reused as a fusion threshold when disabling rerank', async ({
  page,
}) => {
  const requests = await fixture(page)
  await page.goto('/#search')
  await page.locator('summary').filter({ hasText: '检索设置' }).click()
  await page.getByLabel('精排最低分数', { exact: false }).fill('0.5')
  await page.getByLabel('融合分最低阈值', { exact: false }).fill('0.001')
  await page.getByLabel('启用模型精排', { exact: false }).uncheck()
  await expect(page.getByLabel('精排最低分数', { exact: false })).toHaveCount(0)
  await page.getByLabel('你想在知识库中查找什么？').fill('供需关系')
  await page.getByRole('button', { name: '开始检索' }).click()
  await expect.poll(() => requests.filter((r) => r.path.endsWith('/search')).length).toBe(1)
  const body = requests.find((r) => r.path.endsWith('/search'))?.body
  expect(body.min_score).toBe(0)
  expect(body.fusion_min_score).toBe(0.001)
  expect(body.rerank_switch).toBe(false)
})
