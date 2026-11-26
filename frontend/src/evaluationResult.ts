export type EvaluationResultInput = {
  status: string
  diagnostic: boolean
  retrievalFailures: number
  boundaryFailures: number
  scoringFailed: boolean
  hasReferenceMetrics: boolean
  report?: { verdict: string; reasons: string[] }
}

// 执行成功、回归通过和生产合格是不同概念，禁止根据分数猜测及格线。
export function evaluationConclusion(input: EvaluationResultInput) {
  if (input.status === 'queued' || input.status === 'running') {
    return {
      title: '正在评估，尚无最终结论',
      reason: '完成后会展示实际计算的指标和缺失项。',
      action: '请等待任务完成。',
      tone: 'pending',
    }
  }
  if (input.status === 'cancelled') {
    return {
      title: '任务已取消',
      reason: '取消发生在评分完成前，本次没有可用于判断的完整结果。',
      action: '重新运行即可；已保存的检索与评分记录不会被覆盖。',
      tone: 'pending',
    }
  }
  if (input.status === 'interrupted') {
    return {
      title: '任务中断',
      reason: '服务重启或执行器退出，本次没有完成。',
      action: '重新运行即可。',
      tone: 'pending',
    }
  }
  if (input.boundaryFailures > 0) {
    return {
      title: '不满足安全检查',
      reason: '出现禁止返回的证据或边界预期未满足。相关性高也不能抵消此问题。',
      action: '先检查失败题的租户、知识库过滤和实际返回证据。',
      tone: 'fail',
    }
  }
  if (input.status !== 'succeeded' || input.retrievalFailures > 0 || input.scoringFailed) {
    return {
      title: '无法判定：评估未完整成功',
      reason: '存在未完成的检索或评分，不能用成功样本均值宣布合格。',
      action: '查看下方失败状态，修复原因后重新运行。',
      tone: 'pending',
    }
  }
  if (input.diagnostic || !input.hasReferenceMetrics) {
    return {
      title: '无法判定：仅完成相关性检查',
      reason: '没有可靠的参考答案，未验证必要信息是否找全，也未验证证据排序。',
      action: '要判断整体效果，请运行已审核的固定题集。',
      tone: 'pending',
    }
  }
  if (input.report?.verdict === 'fail') {
    return {
      title: '不满足本次回归门槛',
      reason: input.report.reasons.join('；') || '与基线比较存在超出门槛的退步。',
      action: '查看退步题与证据；本报告不自动部署。',
      tone: 'fail',
    }
  }
  if (input.report?.verdict === 'pass') {
    return {
      title: '满足本次回归门槛',
      reason: '在可比题集与裁判下，未触发本次设置的退步规则。这不是生产质量认证。',
      action: '仍需结合业务绝对质量要求、独立保留集及安全测试决定发布。',
      tone: 'pass',
    }
  }
  return {
    title: '评分完成，整体是否合格尚未判定',
    reason:
      input.report?.reasons.join('；') ||
      '尚无可用基线比较与业务验收标准，不能仅凭几个分数宣布合格。',
    action: '首轮确认评分可信后设为基线；后续运行再作回归比较。',
    tone: 'pending',
  }
}
