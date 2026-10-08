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
//
// 两条查询都必须经过 Session(&gorm.Session{})：gorm v1.25.1 的 Count 会把
// Statement.Selects 置为 ["count(*)"]，且在返回后不清除，而 db 是这两条查询
// 共享的同一个 *gorm.DB 实例。直接复用该链会让随后的 Find 也被编译成
// SELECT count(*)，此时接口返回的 total 正确、列表却恒为空（已用 DryRun 探针实测确认）。
// Session 会克隆 Statement，使两条查询互不影响。
func (r *KnowledgeBaseRepo) ListByUserID(ctx context.Context, userID string, page, pageSize int) (total int64, list []*knowledgeentity.KnowledgeBase, err error) {
	db := r.provider.UseDB(ctx).Table(knowledgeentity.KnowledgeBase{}.TableName()).
		Where("user_id = ?", userID)

	if err = db.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return 0, nil, err
	}

	offset := (page - 1) * pageSize
	if offset < 0 {
		offset = 0
	}

	err = db.Session(&gorm.Session{}).Order("created_at DESC").Limit(pageSize).Offset(offset).Find(&list).Error
	return total, list, err
}

// Update 更新知识库可变字段
//
// 必须用 Model 而非 Table：gorm 仅在 stmt.Schema 非空时才会为 autoUpdateTime
// 字段补 updated_at，而 Table + Updates(map) 的 Schema 由 map 类型推导、不含字段，
// 会导致 updated_at 永远不刷新（见 gorm callbacks.ConvertToAssignments）。
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
