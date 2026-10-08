package knowledge

import (
	"context"
	"errors"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	knowledgeentity "github.com/PycMono/FastRAG/domain/entity/knowledge"
	"github.com/PycMono/FastRAG/domain/repository"
	knowledgerepo "github.com/PycMono/FastRAG/domain/repository/knowledge"
	sqlsdk "github.com/PycMono/go-mysql-sdk"
	"gorm.io/gorm"
)

// KnowledgeBaseRepo 知识库 MySQL 仓储实现
type KnowledgeBaseRepo struct {
	provider  sqlsdk.Provider
	idService repository.IIDService
}

// NewKnowledgeBaseRepo 创建知识库仓储
func NewKnowledgeBaseRepo(provider sqlsdk.Provider, idService repository.IIDService) knowledgerepo.IKnowledgeBaseRepo {
	return &KnowledgeBaseRepo{provider: provider, idService: idService}
}

// Create 创建知识库
func (r *KnowledgeBaseRepo) Create(ctx context.Context, kb *knowledgeentity.KnowledgeBase) error {
	if kb.ID == "" {
		kb.ID = r.idService.NextID()
	}
	return r.provider.UseDB(ctx).Table(knowledgeentity.KnowledgeBase{}.TableName()).Create(kb).Error
}

// GetByIDAndUserID 按 ID 和用户 ID 查询知识库
// 查不到时归一化为 ErrKnowledgeBaseNotFound，避免 gorm 错误泄漏到上层
func (r *KnowledgeBaseRepo) GetByIDAndUserID(ctx context.Context, id, userID string) (*knowledgeentity.KnowledgeBase, error) {
	var kb knowledgeentity.KnowledgeBase
	err := r.provider.UseDB(ctx).Table(knowledgeentity.KnowledgeBase{}.TableName()).
		Where("id = ? AND user_id = ?", id, userID).
		First(&kb).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.ErrKnowledgeBaseNotFound
	}
	if err != nil {
		return nil, err
	}
	return &kb, nil
}

// ListByUserID 按用户查询知识库列表（创建时间倒序）
func (r *KnowledgeBaseRepo) ListByUserID(ctx context.Context, userID string, page, pageSize int) (total int64, list []*knowledgeentity.KnowledgeBase, err error) {
	db := r.provider.UseDB(ctx).Table(knowledgeentity.KnowledgeBase{}.TableName()).
		Where("user_id = ?", userID)

	if err = db.Count(&total).Error; err != nil {
		return 0, nil, err
	}

	offset := (page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}

	err = db.Order("created_at DESC").Limit(pageSize).Offset(offset).Find(&list).Error
	return total, list, err
}

// Update 更新知识库可变字段
//
// 必须用 Model 而非 Table：gorm 仅在 stmt.Schema 非空时才会为 autoUpdateTime
// 字段补 updated_at，而 Table + Updates(map) 时 Schema 为 nil（map 解析不出结构体
// Schema），会导致 updated_at 永远不刷新（见 gorm callbacks.ConvertToAssignments）。
func (r *KnowledgeBaseRepo) Update(ctx context.Context, kb *knowledgeentity.KnowledgeBase) error {
	return r.provider.UseDB(ctx).Model(&knowledgeentity.KnowledgeBase{}).
		Where("id = ? AND user_id = ?", kb.ID, kb.UserID).
		Updates(map[string]interface{}{
			"name":            kb.Name,
			"description":     kb.Description,
			"embedding_model": kb.EmbeddingModel,
			"status":          kb.Status,
		}).Error
}

// DeleteByIDAndUserID 按 ID 和用户 ID 删除知识库
func (r *KnowledgeBaseRepo) DeleteByIDAndUserID(ctx context.Context, id, userID string) error {
	return r.provider.UseDB(ctx).Table(knowledgeentity.KnowledgeBase{}.TableName()).
		Where("id = ? AND user_id = ?", id, userID).
		Delete(&knowledgeentity.KnowledgeBase{}).Error
}
