import { expect, test } from '@playwright/test'

test('评测页通过管理接口运行并展示评分，不将失败显示为零分', async ({ page }) => {
  const run = {
    id: '20261125-000000-abcdef',
    experiment_name: 'retrieval-pilot',
    status: 'succeeded',
    done: 1,
    expected: 1,
    retrieval_failures: 0,
    boundary_failures: 0,
    rerank_degraded: 0,
    summary: {
      'hybrid/ContextRecall': { mean: 0.75, ok: 1, errors: 0, not_applicable: 0, coverage: 1 },
    },
  }
  await page.route('**/ready', (route) => route.fulfill({ json: { status: 'ready', checks: {} } }))
  await page.route('**/api/admin/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    let data: unknown = null
    if (path.endsWith('/status')) data = {}
    else if (path.endsWith('/drafts')) data = []
    else if (path.endsWith('/datasets'))
      data = [{ name: 'pilot', version: 'v1', cases: 1, profiles: 1, search_requests: 1 }]
    else if (path.endsWith('/items'))
      data = {
        items: [
          {
            case_id: 'q1',
            question: '测试问题',
            reference: '标准证据',
            profile: 'hybrid',
            retrieval_status: 'ok',
            retrieval_latency_ms: 2,
            scoring_latency_ms: 3,
            boundary_violation: false,
            score: {
              metrics: [
                { name: 'ContextRecall', status: 'ok', value: 0.75 },
                {
                  name: 'ContextPrecision',
                  status: 'timeout',
                  value: null,
                  error: 'judge timeout',
                },
              ],
            },
          },
        ],
        total: 1,
      }
    else if (path.endsWith('/runs'))
      data = route.request().method() === 'POST' ? { run_id: run.id } : [run]
    else data = run
    await route.fulfill({
      status: route.request().method() === 'POST' ? 202 : 200,
      json: { code: 0, data },
    })
  })
  page.on('dialog', (dialog) => dialog.accept())
  await page.goto('/#evaluation')
  await page.getByLabel('管理令牌').fill('x'.repeat(32))
  await page.getByRole('button', { name: '验证并继续' }).click()
  await page.getByRole('tab', { name: '运行评估', exact: true }).click()
  await expect(page.getByText('1 题 × 1 组配置 = 1 次检索', { exact: false })).toBeVisible()
  await page.getByRole('button', { name: '运行评测', exact: true }).click()
  await page.getByText('查看完整指标统计与计算口径').click()
  await expect(page.getByRole('cell', { name: '0.750', exact: true })).toBeVisible()
  await expect(page.getByText('证据排序质量：超时 / — judge timeout')).toBeVisible()
})

test('审核题目分页、筛选并保留跨页未保存修改', async ({ page }) => {
  let draft = {
    id: 'pagination-draft',
    version: 1,
    tenant: 'home',
    kb_no: 'econ',
    items: Array.from({ length: 12 }, (_, i) => ({
      id: `q${i + 1}`,
      question: `问题${i + 1}`,
      reference: '参考答案',
      doc_id: '1',
      doc_name: '测试文档',
      source: '用于核对的原文',
      status: 'draft',
      critical: false,
    })),
  }
  await page.route('**/ready', (route) => route.fulfill({ json: { status: 'ready', checks: {} } }))
  await page.route('**/api/admin/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    let data: unknown = path.endsWith('/drafts') ? [draft] : path.endsWith('/status') ? {} : []
    if (path.endsWith('/pagination-draft') && route.request().method() === 'PUT') {
      const body = route.request().postDataJSON()
      draft = { ...draft, version: draft.version + 1, items: body.items }
      data = draft
    }
    await route.fulfill({ json: { code: 0, data } })
  })
  await page.goto('/#evaluation')
  await page.getByLabel('管理令牌').fill('x'.repeat(32))
  await page.getByRole('button', { name: '验证并继续' }).click()
  await page.getByRole('tab', { name: '准备测试题', exact: true }).click()
  await page.getByText('管理题集与高级设置', { exact: true }).click()
  await page.getByRole('combobox', { name: '切换题集', exact: true }).selectOption(draft.id)
  await page.getByRole('button', { name: '继续核对 →', exact: true }).click()
  await page.getByRole('button', { name: '题集设置', exact: true }).click()
  await page.getByLabel('继续审核已有草稿').selectOption(draft.id)
  await expect(page.locator('.review-item')).toHaveCount(1)
  const reviewHeight = await page.locator('.review-columns').evaluate(element => element.getBoundingClientRect().height)
  await page.getByRole('button', { name: '修改问答', exact: true }).click()
  await page.getByLabel('问题', { exact: true }).first().fill('修改后的问题')
  await page.getByRole('button', { name: '下一题', exact: true }).click()
  await page.getByRole('button', { name: '修改问答', exact: true }).click()
  await expect(page.getByLabel('问题', { exact: true }).first()).toHaveValue('问题2')
  await page.getByRole('button', { name: '上一题', exact: true }).click()
  await page.getByRole('button', { name: '修改问答', exact: true }).click()
  await expect(page.getByLabel('问题', { exact: true }).first()).toHaveValue('修改后的问题')
  await page.getByLabel('参考答案', { exact: true }).first().fill('很长的答案。'.repeat(300))
  await expect(page.locator('.review-columns')).toHaveJSProperty('clientHeight', reviewHeight)
  await page.getByText('更多审核选项', { exact: true }).first().click()
  await page.getByLabel('人工结论').first().selectOption('approved')
  await page.getByLabel('审核状态').selectOption('approved')
  await expect(page.locator('.review-item')).toHaveCount(1)
  await expect(page.getByRole('button', { name: '保存题集，去评估 →' })).toBeDisabled()
  await page.getByRole('tab', { name: '运行评估', exact: true }).click()
  await page.getByRole('tab', { name: '准备测试题', exact: true }).click()
  await expect(page.getByLabel('问题', { exact: true }).first()).toHaveValue('修改后的问题')
  await page.getByLabel('审核状态').selectOption('all')
  await page.getByLabel('每页题数').selectOption('10')
  await expect(page.locator('.review-item')).toHaveCount(10)
  await page.getByLabel('搜索题目或文档').fill('不存在的题目')
  await expect(page.getByText('没有符合条件的题目，试试切换审核状态或清空搜索。')).toBeVisible()
})

test('业务候选题展示原文，必须人工通过后才发布', async ({ page }) => {
  let draft = {
    id: 'draft-test',
    version: 1,
    tenant: 'home',
    kb_no: 'econ',
    items: [
      {
        id: 'q1',
        question: '机会成本是什么？',
        reference: '选择所放弃的最佳替代。',
        doc_id: '1',
        doc_name: '经济学',
        source: '原文：机会成本是为了得到某种东西所必须放弃的东西。',
        status: 'draft',
        critical: false,
      },
    ],
  }
  await page.route('**/ready', (route) => route.fulfill({ json: { status: 'ready', checks: {} } }))
  await page.route('**/api/admin/**', async (route) => {
    const path = new URL(route.request().url()).pathname
    let data: unknown = []
    if (path.endsWith('/status')) data = {}
    else if (path.endsWith('/drafts')) data = route.request().method() === 'POST' ? draft : [draft]
    else if (path.endsWith('/draft-test') && route.request().method() === 'PUT') {
      const body = route.request().postDataJSON()
      draft = { ...draft, items: body.items, version: draft.version + 1 }
      data = draft
    } else if (path.endsWith('/publish')) data = { dataset: 'reviewed-v1' }
    await route.fulfill({ json: { code: 0, data } })
  })
  page.on('dialog', (dialog) => dialog.accept())
  await page.goto('/#evaluation')
  await page.getByLabel('管理令牌').fill('x'.repeat(32))
  await page.getByRole('button', { name: '验证并继续' }).click()
  await page.getByRole('tab', { name: '准备测试题', exact: true }).click()
  await page.getByText('管理题集与高级设置', { exact: true }).click()
  await page.getByRole('combobox', { name: '切换题集', exact: true }).selectOption(draft.id)
  await page.getByRole('button', { name: '继续核对 →', exact: true }).click()
  await expect(page.getByText('原文：机会成本是为了得到某种东西所必须放弃的东西。')).toBeVisible()
  // 模拟出题任务在用户审核期间更新版本，审核保存必须合并而非卡住或覆盖。
  draft.version += 1
  await page.getByRole('button', { name: '通过并继续' }).click()
  await expect(page.getByText('审核已保存', { exact: true })).toBeVisible()
  expect(draft.items[0]?.status).toBe('approved')
  await page.getByRole('button', { name: '撤销上次审核', exact: true }).click()
  await expect(page.getByText('已撤销上一次审核', { exact: true })).toBeVisible()
  expect(draft.items[0]?.status).toBe('draft')
})
