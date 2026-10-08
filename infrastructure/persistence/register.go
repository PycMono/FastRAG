package persistence

import (
	"github.com/PycMono/FastRAG/domain/repository"
	knowledgerepo "github.com/PycMono/FastRAG/domain/repository/knowledge"
	knowledgepersistence "github.com/PycMono/FastRAG/infrastructure/persistence/knowledge"
	sqlsdk "github.com/PycMono/go-mysql-sdk"
	"go.uber.org/fx"
)

// Register 注册所有持久化实现
var Register = fx.Options(
	// Knowledge 模块（MySQL 实现）
	fx.Provide(func(provider sqlsdk.Provider, idService repository.IIDService) knowledgerepo.IKnowledgeBaseRepo {
		return knowledgepersistence.NewKnowledgeBaseRepo(provider, idService)
	}),

	// Knowledge 检索（占位实现，接入向量库时替换）
	fx.Provide(knowledgepersistence.NewRetrieverStub),
)
