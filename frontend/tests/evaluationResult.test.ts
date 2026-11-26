import { describe, expect, it } from 'vitest'
import { evaluationConclusion } from '../src/evaluationResult'

const complete = {
  status: 'succeeded',
  diagnostic: false,
  retrievalFailures: 0,
  boundaryFailures: 0,
  scoringFailed: false,
  hasReferenceMetrics: true,
}

describe('评估结论不混淆执行成功和质量合格', () => {
  it('单题相关性满分也不能判合格', () => {
    expect(evaluationConclusion({ ...complete, diagnostic: true }).title).toContain('无法判定')
  })
  it('无基线时不给合格结论', () => {
    expect(evaluationConclusion(complete).title).toContain('尚未判定')
  })
  it('完整可比报告只称回归通过', () => {
    expect(
      evaluationConclusion({ ...complete, report: { verdict: 'pass', reasons: [] } }).title,
    ).toBe('满足本次回归门槛')
  })
  it('失败、安全问题不被旧通过报告掩盖', () => {
    expect(
      evaluationConclusion({
        ...complete,
        scoringFailed: true,
        report: { verdict: 'pass', reasons: [] },
      }).tone,
    ).toBe('pending')
    expect(evaluationConclusion({ ...complete, boundaryFailures: 1 }).tone).toBe('fail')
  })
})
