package doc

import (
	"fmt"

	"github.com/PycMono/FastRAG/application/service/ingest"
	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	ginsdk "github.com/PycMono/go-gin-sdk"
	"github.com/gin-gonic/gin"
)

// maxBatchDocs 单次批量导入的文档数上限。
const maxBatchDocs = 100

// Controller 文档导入 HTTP 控制器。
//
// 注意这里**没有任何鉴权中间件**：account 就是请求体里的普通字段（§6）。
// 部署时 FastRAG 只能在可信内网暴露，调用方自己负责对最终用户鉴权。
type Controller struct {
	service *ingest.Service
}

func NewController(service *ingest.Service) *Controller {
	return &Controller{service: service}
}

// Ingest POST /api/v1/docs
func (ctl *Controller) Ingest(c *gin.Context) {
	var param dto.DocIngestDTO
	if err := c.ShouldBindJSON(&param); err != nil {
		ginsdk.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	result, err := ctl.service.Ingest(c.Request.Context(), &param)
	ginsdk.Send(c, result, err)
}

// BatchIngest POST /api/v1/docs/batch
//
// 请求体是一个数组。**恒返回 200 + 明细**，单条失败不改 HTTP 状态码——
// 否则调用方没法区分「全挂了」和「挂了 3 条」，只能整批重试，
// 而整批重试会把已经成功的那 97 条再灌一遍。
func (ctl *Controller) BatchIngest(c *gin.Context) {
	var params []*dto.DocIngestDTO
	if err := c.ShouldBindJSON(&params); err != nil {
		ginsdk.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}
	if len(params) == 0 {
		ginsdk.Send(c, nil, apperrors.NewParamError("批量导入列表为空"))
		return
	}
	if len(params) > maxBatchDocs {
		ginsdk.Send(c, nil, apperrors.NewParamError(fmt.Sprintf(
			"单次最多 %d 篇，收到 %d 篇；请上游分批调用", maxBatchDocs, len(params))))
		return
	}

	ginsdk.Send(c, ctl.service.BatchIngest(c.Request.Context(), params), nil)
}

// Delete POST /api/v1/docs/delete
//
// 用 POST 而不是 DELETE：DELETE 带 body 是合法的，但一些网关与
// 客户端库会把它丢掉，最后收到一个「参数为空」的 400，很难排查。
func (ctl *Controller) Delete(c *gin.Context) {
	var param dto.DocDeleteDTO
	if err := c.ShouldBindJSON(&param); err != nil {
		ginsdk.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	result, err := ctl.service.DeleteDoc(c.Request.Context(), &param)
	ginsdk.Send(c, result, err)
}
