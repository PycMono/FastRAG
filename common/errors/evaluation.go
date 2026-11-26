package errors

// 评测模块沿用统一业务错误协议，不在控制器自行拼装 HTTP 错误体。
var (
	ErrEvaluationDisabled = NewBizError(10601, "评测功能未开启")
	ErrEvaluationBusy     = NewBizError(CodeConflict, "已有评测任务运行")
	ErrEvaluationInvalid  = NewBizError(CodeInvalidParam, "评测输入无效")
	ErrEvaluationNotFound = NewBizError(CodeNotFound, "评测记录不存在")
	ErrEvaluationFailed   = NewSysError(10602, "评测操作失败，请检查服务端配置与日志")
)
