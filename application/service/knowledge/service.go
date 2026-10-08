package knowledge

import (
	"context"

	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/common/vo"
	knowledgeentity "github.com/PycMono/FastRAG/domain/entity/knowledge"
	"github.com/PycMono/FastRAG/domain/repository"
	knowledgerepo "github.com/PycMono/FastRAG/domain/repository/knowledge"
)

// Service 知识库应用服务
type Service struct {
	repo      knowledgerepo.IKnowledgeBaseRepo
	retriever knowledgerepo.IKnowledgeRetriever
	idService repository.IIDService
}

// NewService 创建知识库服务（fx 注入）
func NewService(
	repo knowledgerepo.IKnowledgeBaseRepo,
	retriever knowledgerepo.IKnowledgeRetriever,
	idService repository.IIDService,
) *Service {
	return &Service{repo: repo, retriever: retriever, idService: idService}
}

// Create 创建知识库
func (s *Service) Create(ctx context.Context, userID string, param dto.CreateKnowledgeBaseDTO) (*vo.KnowledgeBaseVO, error) {
	kb := &knowledgeentity.KnowledgeBase{
		ID:             s.idService.NextID(),
		UserID:         userID,
		Name:           param.Name,
		Description:    param.Description,
		EmbeddingModel: param.EmbeddingModel,
		Status:         knowledgeentity.StatusEnabled,
	}
	if err := s.repo.Create(ctx, kb); err != nil {
		return nil, apperrors.ErrKnowledgeBaseCreateFailed.Wrap(err)
	}
	return toVO(kb), nil
}

// Get 查询单个知识库
func (s *Service) Get(ctx context.Context, userID, id string) (*vo.KnowledgeBaseVO, error) {
	kb, err := s.repo.GetByIDAndUserID(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	return toVO(kb), nil
}

// List 分页查询知识库列表
func (s *Service) List(ctx context.Context, userID string, query dto.ListKnowledgeBaseQuery) (*vo.KnowledgeBaseListVO, error) {
	page := query.Page
	if page < 1 {
		page = 1
	}
	pageSize := query.PageSize
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}

	total, list, err := s.repo.ListByUserID(ctx, userID, page, pageSize)
	if err != nil {
		return nil, err
	}

	vos := make([]*vo.KnowledgeBaseVO, 0, len(list))
	for _, kb := range list {
		vos = append(vos, toVO(kb))
	}
	return vo.NewPageResult(total, vos, page, pageSize), nil
}

// Update 更新知识库（仅更新请求中提供了的字段）
func (s *Service) Update(ctx context.Context, userID, id string, param dto.UpdateKnowledgeBaseDTO) error {
	kb, err := s.repo.GetByIDAndUserID(ctx, id, userID)
	if err != nil {
		return err
	}

	if param.Name != nil {
		kb.Name = *param.Name
	}
	if param.Description != nil {
		kb.Description = *param.Description
	}
	if param.EmbeddingModel != nil {
		kb.EmbeddingModel = *param.EmbeddingModel
	}
	if param.Status != nil {
		kb.Status = *param.Status
	}

	if err := s.repo.Update(ctx, kb); err != nil {
		return apperrors.ErrKnowledgeBaseUpdateFailed.Wrap(err)
	}
	return nil
}

// Delete 删除知识库
func (s *Service) Delete(ctx context.Context, userID, id string) error {
	if err := s.repo.DeleteByIDAndUserID(ctx, id, userID); err != nil {
		return apperrors.ErrKnowledgeBaseDeleteFailed.Wrap(err)
	}
	return nil
}

// Search 知识库检索（扩展点）
// 先校验知识库归属，避免越权检索；实际检索委托给 IKnowledgeRetriever 实现。
func (s *Service) Search(ctx context.Context, userID, id string, param dto.SearchKnowledgeBaseDTO) ([]*vo.RetrievedChunkVO, error) {
	if _, err := s.repo.GetByIDAndUserID(ctx, id, userID); err != nil {
		return nil, err
	}

	chunks, err := s.retriever.Retrieve(ctx, knowledgerepo.RetrieveQuery{
		KnowledgeBaseID: id,
		Query:           param.Query,
		TopK:            param.TopK,
	})
	if err != nil {
		return nil, err
	}

	vos := make([]*vo.RetrievedChunkVO, 0, len(chunks))
	for _, c := range chunks {
		vos = append(vos, &vo.RetrievedChunkVO{
			DocID:      c.DocID,
			ChunkIndex: c.ChunkIndex,
			Content:    c.Content,
			Score:      c.Score,
		})
	}
	return vos, nil
}

// toVO 实体转响应对象
func toVO(kb *knowledgeentity.KnowledgeBase) *vo.KnowledgeBaseVO {
	return &vo.KnowledgeBaseVO{
		ID:             kb.ID,
		UserID:         kb.UserID,
		Name:           kb.Name,
		Description:    kb.Description,
		EmbeddingModel: kb.EmbeddingModel,
		DocCount:       kb.DocCount,
		Status:         kb.Status,
		CreatedAt:      kb.CreatedAt,
		UpdatedAt:      kb.UpdatedAt,
	}
}
