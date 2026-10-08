# FastRAG 后端骨架 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为 FastRAG（知识库检索）搭建与 micro-framework 同构的 Go 后端骨架：洋葱架构分层、Uber FX 装配、统一错误码与响应、完整的 knowledge 示例模块（MySQL 全链路 CRUD + 检索扩展点）。

**Architecture:** 洋葱架构，外层依赖内层。`domain` 定义实体与仓储接口；`application/service` 编排用例并做 DTO→Entity→VO 转换；`infrastructure` 提供 HTTP 入口、仓储实现与 SDK 适配；`common` 存放跨层 DTO/VO/错误码。各层通过 `register.go` 暴露 `fx.Options`，由 `infrastructure/init.go` 汇总装配，`cmd/server/main.go` 用 `fx.Lifecycle` 启动 AutoMigrate 与 HTTP Server。

**Tech Stack:** Go 1.25、Gin 1.10、Uber FX 1.23、GORM 1.25、go-gin-sdk v0.0.6、go-mysql-sdk v1.0.2、go-cache-sdk v1.0.3、go-logger-sdk v1.0.5、go-context-sdk v1.0.2、bwmarrin/snowflake

**Spec:** `docs/superpowers/specs/2026-10-08-fastrag-backend-scaffold-design.md`

## Global Constraints

- 模块路径：`github.com/PycMono/FastRAG`（所有内部 import 均以此开头）。
- Go 版本：`go 1.25.0`。
- **本机 Go 不在 PATH 上**，每条命令前必须导出环境（下文命令已内联）：
  ```bash
  export PATH="/Users/allen/projects/gosdk/go1.25.8/bin:$PATH"
  export GOPATH=/tmp/gopath GOMODCACHE=/tmp/gopath/pkg/mod GOFLAGS=-mod=mod
  export GOPRIVATE="github.com/PycMono/*"
  ```
- 分层红线（`scripts/lint-architecture.sh` 会强制检查）：
  - `domain/` **禁止** import `gin` / `gorm` / `redis` / `net/http` / `infrastructure/...` / `application/...`
  - `application/` **禁止** import `gin` / `infrastructure/driver/...` / `infrastructure/controller`
  - `common/` **禁止** import `domain/...` / `application/...` / `infrastructure/...`
- Controller **必须**用 `gingext.Send(c, data, err)` 返回，**禁止**裸 `c.JSON(`（health 豁免）。
- 所有 JSON API 路由**必须**注册在 `api := router.Group(v1Prefix)` 分组内；`v1Prefix = "/api/v1"`。
- 错误码：通用段 `10001–10007`，knowledge 段 `10101–10106`（具名常量定义在 `common/errors`，禁止散落魔法数字）。
- 分页统一用 `dto.PageQuery` + `vo.PageResult[T]` / `vo.NewPageResult`，不手写 offset/limit。
- 实体主键为 **string 雪花 ID**，时间戳字段为 **int64 毫秒**。
- 代码注释用中文，与 micro-framework 保持一致。
- 私有 SDK 的 `redis.UniversalClient` 实际类型是 `github.com/redis/go-redis/v9.UniversalClient`，适配层需直接 import 该包。
- **不要**引入前端相关代码：无 `frontend/`、无 `infrastructure/controller/http/page/`、无模板渲染、无静态资源、无 i18n。

---

## 文件结构

| 文件 | 职责 |
|------|------|
| `go.mod` / `config.json` / `.gitignore` / `Makefile` | 工程基础 |
| `infrastructure/config/config.go` | 配置结构体 + `config.json` 加载 |
| `common/errors/errors.go` | 错误码常量、`CodeError` 接口、`BizError`/`SysError`、预定义错误 |
| `common/dto/pagination.go` · `common/vo/pagination.go` | 通用分页入参/出参 |
| `common/dto/knowledge.go` · `common/vo/knowledge.go` | knowledge 模块入参/出参 |
| `domain/entity/knowledge/knowledge_base.go` | 知识库实体（GORM 模型） |
| `domain/repository/knowledge/knowledge_base.go` | 知识库仓储接口 |
| `domain/repository/knowledge/retriever.go` | 检索扩展点接口 + 值对象 |
| `domain/repository/id_service.go` | ID 生成接口 |
| `domain/register.go` · `domain/service/register.go` | 领域层 fx 装配 |
| `infrastructure/middleware/rate_limit.go` | IP 级限流 |
| `infrastructure/middleware/access_log.go` | 访问日志 |
| `infrastructure/driver/gingext/response.go` | 统一响应封装 `Send` |
| `infrastructure/driver/gingext/gingext.go` | Gin 引擎（中间件链）+ HTTP Server |
| `infrastructure/driver/mysql/mysql.go` | `sqlsdk.Provider` 适配 |
| `infrastructure/driver/redis/redis.go` | Redis 客户端（可空） |
| `infrastructure/serviceimpl/id_service.go` + `register.go` | 雪花 ID 实现 |
| `infrastructure/persistence/knowledge/knowledge_base_repo.go` | 仓储 MySQL 实现 |
| `infrastructure/persistence/knowledge/retriever_stub.go` | 检索占位实现 |
| `infrastructure/persistence/register.go` | 持久化层 fx 装配 |
| `application/service/knowledge/service.go` | 知识库应用服务 |
| `application/service/register.go` | 应用层 fx 装配 |
| `infrastructure/controller/http/health/controller.go` | 健康/就绪探针 |
| `infrastructure/controller/http/knowledge/controller.go` | 知识库 HTTP 控制器 |
| `infrastructure/controller/http/register.go` | 路由注册（`RouteDeps` + `api` 分组） |
| `infrastructure/controller/register.go` | 控制器层 fx 装配 |
| `infrastructure/init.go` | 全量 fx 装配总入口 |
| `cmd/server/main.go` | 启动入口 |
| `scripts/lint-architecture.sh` | 分层红线 linter |
| `README.md` | 分层说明 / 快速开始 / 错误码 / 新增模块指引 |

**任务顺序按依赖自底向上排列**，每个任务结束时 `go build ./...` 均通过。

---

### Task 1: 工程基础与配置加载

**Files:**
- Create: `go.mod`, `config.json`, `.gitignore`, `Makefile`
- Create: `infrastructure/config/config.go`

**Interfaces:**
- Consumes: 无
- Produces: `config.Config`（字段 `App string`、`Debug bool`、`HTTP HTTPConfig`、`MySQL MySQLConfig`、`Redis RedisConfig`、`SnowflakeNodeID int`）、`config.HTTPConfig{Host,Port string; ReadTimeout,WriteTimeout int}`、`config.MySQLConfig`、`config.RedisConfig`、`config.MustLoad() *config.Config`、`config.Load() (*config.Config, error)`、`config.Get() *Config`

- [ ] **Step 1: 写 `go.mod`**

```
module github.com/PycMono/FastRAG

go 1.25.0

require (
	github.com/PycMono/go-cache-sdk v1.0.3
	github.com/PycMono/go-context-sdk v1.0.2
	github.com/PycMono/go-gin-sdk v0.0.6
	github.com/PycMono/go-logger-sdk v1.0.5
	github.com/PycMono/go-mysql-sdk v1.0.2
	github.com/bwmarrin/snowflake v0.3.0
	github.com/gin-gonic/gin v1.10.0
	github.com/redis/go-redis/v9 v9.19.0
	go.uber.org/fx v1.23.0
	gorm.io/gorm v1.25.1
)
```

- [ ] **Step 2: 写 `config.json`**

```json
{
  "app": "fastrag",
  "debug": true,
  "http": {
    "host": "",
    "port": "8080",
    "read_timeout": 120,
    "write_timeout": 120
  },
  "mysql": {
    "host": "127.0.0.1",
    "port": 3306,
    "database": "fastrag",
    "user": "root",
    "password": "123456",
    "max_open": 100,
    "max_idle": 10,
    "conn_lifetime": 3600,
    "conn_timeout": 3,
    "log_level": 3,
    "slow_threshold": 500
  },
  "redis": {
    "addr": [],
    "password": "",
    "db": 0,
    "pool_size": 5
  },
  "snowflake_node_id": 1
}
```

- [ ] **Step 3: 写 `.gitignore`**

```
# Binaries for programs and plugins
*.exe
*.exe~
*.dll
*.so
*.dylib

# Test binary, built with `go test -c`
*.test

# Output of the go coverage tool
*.out
coverage.html

# Dependency directories
vendor/
node_modules/

# Build output
bin/
dist/

# IDE
.idea/
.vscode/
*.swp
*.swo
*~

# OS
.DS_Store
Thumbs.db

# Config (local overrides)
config.local.json
config.dev.json
config.prod.json
server

main
```

- [ ] **Step 4: 写 `Makefile`**

```makefile
.PHONY: build run debug test test-pkg test-single lint clean tidy

# 构建项目
build:
	go build -o bin/server ./cmd/server

# 运行服务
run:
	go run ./cmd/server

# 调试模式启动后端（等待 Delve 连接，端口 2345）
debug:
	@dlv debug ./cmd/server --headless --listen=:2345 --api-version=2 --accept-multiclient

# 运行所有测试
test:
	go test -v -race -cover ./...

# 运行指定包的测试
test-pkg:
	go test -v -race -cover ./$(PKG)/...

# 运行单个测试
test-single:
	go test -v -run $(NAME) ./$(PKG)/...

# 架构红线检查
lint:
	bash scripts/lint-architecture.sh

# 清理构建产物
clean:
	rm -rf bin/

# 整理依赖
tidy:
	go mod tidy
```

- [ ] **Step 5: 写 `infrastructure/config/config.go`**

```go
package config

import (
	"encoding/json"
	"os"
)

var conf *Config

// Config 配置结构
type Config struct {
	App             string      `json:"app"`               // 应用名称
	Debug           bool        `json:"debug"`             // 调试模式
	HTTP            HTTPConfig  `json:"http"`              // HTTP 配置
	MySQL           MySQLConfig `json:"mysql"`             // MySQL 配置
	Redis           RedisConfig `json:"redis"`             // Redis 配置
	SnowflakeNodeID int         `json:"snowflake_node_id"` // 雪花 ID 节点号 [0, 1023]
}

// HTTPConfig HTTP 配置
type HTTPConfig struct {
	Host         string `json:"host"`          // 监听主机，空字符串表示监听 0.0.0.0
	Port         string `json:"port"`          // 监听端口
	ReadTimeout  int    `json:"read_timeout"`  // 读取超时（秒）
	WriteTimeout int    `json:"write_timeout"` // 写入超时（秒）
}

// MySQLConfig MySQL 配置
type MySQLConfig struct {
	Host          string `json:"host"`           // 主机
	Port          int    `json:"port"`           // 端口
	Database      string `json:"database"`       // 数据库名
	User          string `json:"user"`           // 用户名
	Password      string `json:"password"`       // 密码
	MaxOpen       int    `json:"max_open"`       // 最大连接数
	MaxIdle       int    `json:"max_idle"`       // 最大空闲连接数
	ConnLifetime  int    `json:"conn_lifetime"`  // 连接最大生命周期（分钟）
	ConnTimeout   int    `json:"conn_timeout"`   // 连接超时（秒）
	LogLevel      int    `json:"log_level"`      // GORM 日志级别
	SlowThreshold int    `json:"slow_threshold"` // 慢查询阈值（毫秒）
}

// RedisConfig Redis 配置
type RedisConfig struct {
	Addr     []string `json:"addr"`      // Redis 地址列表，为空表示不启用
	Password string   `json:"password"`  // 密码
	DB       int      `json:"db"`        // 数据库编号
	PoolSize int      `json:"pool_size"` // 连接池大小
}

// MustLoad 加载配置，失败则 panic
func MustLoad() *Config {
	c, err := Load()
	if err != nil {
		panic(err)
	}
	return c
}

// Load 从 config.json 加载配置
func Load() (*Config, error) {
	data, err := os.ReadFile("config.json")
	if err != nil {
		return nil, err
	}

	c := &Config{}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, err
	}

	conf = c
	return c, nil
}

// Get 获取全局配置
func Get() *Config {
	return conf
}
```

- [ ] **Step 6: 拉取依赖并验证编译**

注意：此处用 `go mod download` 而不是 `go mod tidy` —— 后续任务才会逐步引入 import，
过早 `tidy` 会把尚未使用的依赖删掉。

```bash
export PATH="/Users/allen/projects/gosdk/go1.25.8/bin:$PATH"
export GOPATH=/tmp/gopath GOMODCACHE=/tmp/gopath/pkg/mod GOFLAGS=-mod=mod
export GOPRIVATE="github.com/PycMono/*"
cd /Users/allen/projects/work/github/FastRAG
go mod download all
go build ./...
```

Expected: 无输出，退出码 0。

- [ ] **Step 7: 提交**

```bash
git add go.mod go.sum config.json .gitignore Makefile infrastructure/config/config.go
git commit -m "chore: 搭建工程基础与配置加载"
```

---

### Task 2: 错误码体系

**Files:**
- Create: `common/errors/errors.go`
- Test: `common/errors/errors_test.go`

**Interfaces:**
- Consumes: 无（纯标准库）
- Produces:
  - 错误码常量：`CodeInvalidParam=10001`、`CodeUnauthorized=10002`、`CodeForbidden=10003`、`CodeNotFound=10004`、`CodeConflict=10005`、`CodeRateLimited=10006`、`CodeInternal=10007`、`CodeKnowledgeBaseNotFound=10101`、`CodeKnowledgeBaseNameExists=10102`、`CodeKnowledgeBaseCreateFail=10103`、`CodeKnowledgeBaseUpdateFail=10104`、`CodeKnowledgeBaseDeleteFail=10105`、`CodeKnowledgeRetrievalNotImpl=10106`
  - 接口 `CodeError interface { error; Code() int; Message() string; Unwrap() error }`
  - `NewBizError(code int, msg string) *BizError`、`NewSysError(code int, msg string) *SysError`
  - `(*BizError).Wrap(err error) *BizError`、`(*BizError).Params(args ...interface{}) *BizError`，`SysError` 同构
  - `AsBizError(err error) (*BizError, bool)`、`AsSysError(err error) (*SysError, bool)`
  - 预定义错误变量：`ErrInvalidParam`、`ErrUnauthorized`、`ErrForbidden`、`ErrNotFound`、`ErrConflict`、`ErrRateLimited`、`ErrInternal`、`ErrKnowledgeBaseNotFound`、`ErrKnowledgeBaseNameExists`、`ErrKnowledgeBaseCreateFailed`、`ErrKnowledgeBaseUpdateFailed`、`ErrKnowledgeBaseDeleteFailed`、`ErrKnowledgeRetrievalNotImpl`

- [ ] **Step 1: 写失败测试 `common/errors/errors_test.go`**

```go
package errors

import (
	"errors"
	"fmt"
	"testing"
)

func TestBizError_CodeAndMessage(t *testing.T) {
	err := NewBizError(CodeInvalidParam, "invalid parameter")

	if err.Code() != CodeInvalidParam {
		t.Fatalf("Code() = %d, want %d", err.Code(), CodeInvalidParam)
	}
	if err.Message() != "invalid parameter" {
		t.Fatalf("Message() = %q, want %q", err.Message(), "invalid parameter")
	}
	if err.Error() != "invalid parameter" {
		t.Fatalf("Error() = %q, want %q", err.Error(), "invalid parameter")
	}
}

func TestCodeError_IsMatchesByCode(t *testing.T) {
	err := ErrInvalidParam.Params("name")

	if !errors.Is(err, ErrInvalidParam) {
		t.Fatal("expected errors.Is(err, ErrInvalidParam) to be true (same code)")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatal("expected errors.Is(err, ErrNotFound) to be false (different code)")
	}
}

func TestBizError_ParamsKeepsCodeAndDoesNotMutateSource(t *testing.T) {
	base := NewBizError(10999, "user %s not found")
	filled := base.Params("u1")

	if filled.Code() != 10999 {
		t.Fatalf("filled.Code() = %d, want 10999", filled.Code())
	}
	if filled.Message() != "user u1 not found" {
		t.Fatalf("filled.Message() = %q, want %q", filled.Message(), "user u1 not found")
	}
	if base.Message() != "user %s not found" {
		t.Fatalf("source error was mutated: %q", base.Message())
	}
}

func TestCodeError_WrapUnwrapsToCause(t *testing.T) {
	cause := errors.New("connection refused")
	err := ErrKnowledgeBaseCreateFailed.Wrap(cause)

	if !errors.Is(err, cause) {
		t.Fatal("expected errors.Is(err, cause) to be true after Wrap")
	}
	if err.Code() != CodeKnowledgeBaseCreateFail {
		t.Fatalf("Code() = %d, want %d", err.Code(), CodeKnowledgeBaseCreateFail)
	}
}

func TestAsBizError_FindsWrappedBizError(t *testing.T) {
	wrapped := fmt.Errorf("bind failed: %w", ErrInvalidParam)

	biz, ok := AsBizError(wrapped)
	if !ok {
		t.Fatal("expected AsBizError to find the wrapped BizError")
	}
	if biz.Code() != CodeInvalidParam {
		t.Fatalf("Code() = %d, want %d", biz.Code(), CodeInvalidParam)
	}
}

func TestAsBizError_ReturnsFalseForSysError(t *testing.T) {
	if _, ok := AsBizError(ErrInternal); ok {
		t.Fatal("expected AsBizError to be false for a SysError")
	}
}

func TestAsSysError_FindsWrappedSysError(t *testing.T) {
	wrapped := fmt.Errorf("query failed: %w", ErrInternal)

	sys, ok := AsSysError(wrapped)
	if !ok {
		t.Fatal("expected AsSysError to find the wrapped SysError")
	}
	if sys.Code() != CodeInternal {
		t.Fatalf("Code() = %d, want %d", sys.Code(), CodeInternal)
	}
}

func TestAsSysError_ReturnsFalseForBizError(t *testing.T) {
	if _, ok := AsSysError(ErrNotFound); ok {
		t.Fatal("expected AsSysError to be false for a BizError")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
export PATH="/Users/allen/projects/gosdk/go1.25.8/bin:$PATH"
export GOPATH=/tmp/gopath GOMODCACHE=/tmp/gopath/pkg/mod GOFLAGS=-mod=mod
export GOPRIVATE="github.com/PycMono/*"
cd /Users/allen/projects/work/github/FastRAG
go test ./common/errors/...
```

Expected: FAIL —— `undefined: NewBizError` 等编译错误（`common/errors/errors.go` 尚不存在）。

- [ ] **Step 3: 写实现 `common/errors/errors.go`**

```go
package errors

import (
	"errors"
	"fmt"
)

// ─── 错误码常量 ──────────────────────────────────────────────────────────────

// 通用错误码
const (
	CodeInvalidParam = 10001 // 参数错误
	CodeUnauthorized = 10002 // 未登录
	CodeForbidden    = 10003 // 无权限
	CodeNotFound     = 10004 // 资源不存在
	CodeConflict     = 10005 // 资源冲突
	CodeRateLimited  = 10006 // 触发限流
	CodeInternal     = 10007 // 服务内部错误
)

// knowledge 模块错误码
const (
	CodeKnowledgeBaseNotFound     = 10101 // 知识库不存在
	CodeKnowledgeBaseNameExists   = 10102 // 知识库名称已存在
	CodeKnowledgeBaseCreateFail   = 10103 // 知识库创建失败
	CodeKnowledgeBaseUpdateFail   = 10104 // 知识库更新失败
	CodeKnowledgeBaseDeleteFail   = 10105 // 知识库删除失败
	CodeKnowledgeRetrievalNotImpl = 10106 // 检索能力未实现
)

// ─── CodeError 基础 ──────────────────────────────────────────────────────────

// CodeError 带错误码的错误接口
type CodeError interface {
	error
	Code() int
	Message() string
	Unwrap() error
}

// codeError 基础实现
type codeError struct {
	code  int
	msg   string
	cause error
}

func newCodeError(code int, msg string, cause error) *codeError {
	return &codeError{
		code:  code,
		msg:   msg,
		cause: cause,
	}
}

func (e *codeError) Code() int       { return e.code }
func (e *codeError) Message() string { return e.msg }
func (e *codeError) Error() string   { return e.msg }
func (e *codeError) Unwrap() error   { return e.cause }

// Is 按 code 判断是否是同一个错误类型
func (e *codeError) Is(target error) bool {
	if ce, ok := target.(CodeError); ok {
		return ce.Code() == e.code
	}
	return errors.Is(e.cause, target)
}

// Params 用格式化参数生成新的错误（保留原 code，不修改原错误）
func (e *codeError) Params(args ...interface{}) *codeError {
	return newCodeError(e.code, fmt.Sprintf(e.msg, args...), e.cause)
}

// Wrap 包装底层错误
func (e *codeError) Wrap(err error) *codeError {
	return newCodeError(e.code, e.msg, err)
}

// ─── BizError 业务错误（预期内）──────────────────────────────────────────────

// BizError 业务错误：由业务规则触发，属于预期内的失败
type BizError struct{ *codeError }

// NewBizError 创建业务错误
func NewBizError(code int, msg string) *BizError {
	return &BizError{newCodeError(code, msg, nil)}
}

// Wrap 包装底层错误
func (e *BizError) Wrap(err error) *BizError {
	return &BizError{e.codeError.Wrap(err)}
}

// Params 用格式化参数生成新的业务错误
func (e *BizError) Params(args ...interface{}) *BizError {
	return &BizError{e.codeError.Params(args...)}
}

// AsBizError 从错误链中提取 BizError
func AsBizError(err error) (*BizError, bool) {
	var biz *BizError
	if errors.As(err, &biz) {
		return biz, true
	}
	return nil, false
}

// ─── SysError 系统错误（非预期）──────────────────────────────────────────────

// SysError 系统错误：基础设施/依赖异常导致，属于非预期失败
type SysError struct{ *codeError }

// NewSysError 创建系统错误
func NewSysError(code int, msg string) *SysError {
	return &SysError{newCodeError(code, msg, nil)}
}

// Wrap 包装底层错误
func (e *SysError) Wrap(err error) *SysError {
	return &SysError{e.codeError.Wrap(err)}
}

// Params 用格式化参数生成新的系统错误
func (e *SysError) Params(args ...interface{}) *SysError {
	return &SysError{e.codeError.Params(args...)}
}

// AsSysError 从错误链中提取 SysError
func AsSysError(err error) (*SysError, bool) {
	var sys *SysError
	if errors.As(err, &sys) {
		return sys, true
	}
	return nil, false
}

// ─── 通用错误 ────────────────────────────────────────────────────────────────

var (
	ErrInvalidParam = NewBizError(CodeInvalidParam, "invalid parameter")
	ErrUnauthorized = NewBizError(CodeUnauthorized, "unauthorized")
	ErrForbidden    = NewBizError(CodeForbidden, "forbidden")
	ErrNotFound     = NewBizError(CodeNotFound, "resource not found")
	ErrConflict     = NewBizError(CodeConflict, "resource conflict")
	ErrRateLimited  = NewBizError(CodeRateLimited, "rate limited")
	ErrInternal     = NewSysError(CodeInternal, "internal server error")
)

// ─── Knowledge 模块 ───────────────────────────────────────────────────────────

var (
	ErrKnowledgeBaseNotFound    = NewBizError(CodeKnowledgeBaseNotFound, "knowledge base not found")
	ErrKnowledgeBaseNameExists  = NewBizError(CodeKnowledgeBaseNameExists, "knowledge base name already exists")
	ErrKnowledgeBaseCreateFailed = NewSysError(CodeKnowledgeBaseCreateFail, "knowledge base create failed")
	ErrKnowledgeBaseUpdateFailed = NewSysError(CodeKnowledgeBaseUpdateFail, "knowledge base update failed")
	ErrKnowledgeBaseDeleteFailed = NewSysError(CodeKnowledgeBaseDeleteFail, "knowledge base delete failed")
	ErrKnowledgeRetrievalNotImpl = NewBizError(CodeKnowledgeRetrievalNotImpl, "knowledge retrieval not implemented")
)
```

- [ ] **Step 4: 运行测试确认通过**

```bash
export PATH="/Users/allen/projects/gosdk/go1.25.8/bin:$PATH"
export GOPATH=/tmp/gopath GOMODCACHE=/tmp/gopath/pkg/mod GOFLAGS=-mod=mod
export GOPRIVATE="github.com/PycMono/*"
cd /Users/allen/projects/work/github/FastRAG
gofmt -l common/errors/ && go test -v ./common/errors/...
```

Expected: `gofmt -l` 无输出；8 个测试全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add common/errors/
git commit -m "feat: 新增错误码体系与 CodeError/BizError/SysError"
```

---

### Task 3: 通用分页 DTO/VO

**Files:**
- Create: `common/dto/pagination.go`
- Create: `common/vo/pagination.go`
- Test: `common/dto/pagination_test.go`
- Test: `common/vo/pagination_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `dto.PageQuery{Page int; PageSize int}`，方法 `Offset() int`、`Limit() int`
  - `vo.PageResult[T any]{Total int64; List []T; Page int; PageSize int; Pages int}`
  - `vo.NewPageResult[T any](total int64, list []T, page, pageSize int) *PageResult[T]`

- [ ] **Step 1: 写失败测试 `common/vo/pagination_test.go`**

```go
package vo

import "testing"

func TestNewPageResult_ExactDivision(t *testing.T) {
	r := NewPageResult(20, []string{"a", "b"}, 1, 10)

	if r.Total != 20 {
		t.Fatalf("Total = %d, want 20", r.Total)
	}
	if r.Pages != 2 {
		t.Fatalf("Pages = %d, want 2", r.Pages)
	}
	if r.Page != 1 || r.PageSize != 10 {
		t.Fatalf("Page/PageSize = %d/%d, want 1/10", r.Page, r.PageSize)
	}
	if len(r.List) != 2 {
		t.Fatalf("len(List) = %d, want 2", len(r.List))
	}
}

func TestNewPageResult_RemainderRoundsUp(t *testing.T) {
	r := NewPageResult(25, []string{}, 3, 10)

	if r.Pages != 3 {
		t.Fatalf("Pages = %d, want 3", r.Pages)
	}
}

func TestNewPageResult_ZeroTotalStillOnePage(t *testing.T) {
	r := NewPageResult(0, []string{}, 1, 10)

	if r.Total != 0 {
		t.Fatalf("Total = %d, want 0", r.Total)
	}
	if r.Pages != 1 {
		t.Fatalf("Pages = %d, want 1", r.Pages)
	}
}

func TestNewPageResult_TotalSmallerThanPageSize(t *testing.T) {
	r := NewPageResult(3, []string{"a"}, 1, 10)

	if r.Pages != 1 {
		t.Fatalf("Pages = %d, want 1", r.Pages)
	}
}
```

- [ ] **Step 2: 写失败测试 `common/dto/pagination_test.go`**

`PageQuery` 属于 `dto` 包，因此必须放在 `package dto` 的测试文件里单独测。

```go
package dto

import "testing"

func TestPageQuery_OffsetAndLimit(t *testing.T) {
	q := PageQuery{Page: 3, PageSize: 10}

	if q.Offset() != 20 {
		t.Fatalf("Offset() = %d, want 20", q.Offset())
	}
	if q.Limit() != 10 {
		t.Fatalf("Limit() = %d, want 10", q.Limit())
	}
}

func TestPageQuery_NormalizesInvalidValues(t *testing.T) {
	q := PageQuery{Page: 0, PageSize: 0}

	if q.Offset() != 0 {
		t.Fatalf("Offset() = %d, want 0", q.Offset())
	}
	if q.Limit() != 10 {
		t.Fatalf("Limit() = %d, want 10 (default)", q.Limit())
	}

	big := PageQuery{Page: 1, PageSize: 500}
	if big.Limit() != 100 {
		t.Fatalf("Limit() = %d, want 100 (capped)", big.Limit())
	}
}

func TestPageQuery_OffsetUsesNormalizedLimit(t *testing.T) {
	// PageSize 超上限时，Offset 必须按归一化后的 100 计算，否则分页会错位
	q := PageQuery{Page: 2, PageSize: 500}

	if q.Offset() != 100 {
		t.Fatalf("Offset() = %d, want 100", q.Offset())
	}
}
```

- [ ] **Step 3: 运行测试确认失败**

```bash
export PATH="/Users/allen/projects/gosdk/go1.25.8/bin:$PATH"
export GOPATH=/tmp/gopath GOMODCACHE=/tmp/gopath/pkg/mod GOFLAGS=-mod=mod
export GOPRIVATE="github.com/PycMono/*"
cd /Users/allen/projects/work/github/FastRAG
go test ./common/dto/... ./common/vo/...
```

Expected: FAIL —— `undefined: NewPageResult`（vo）、`undefined: PageQuery`（dto）。

- [ ] **Step 4: 写 `common/dto/pagination.go`**

```go
package dto

// PageQuery 通用分页查询参数
type PageQuery struct {
	Page     int `form:"page,default=1" json:"page" binding:"gte=1"`
	PageSize int `form:"page_size,default=10" json:"page_size" binding:"gte=1,lte=100"`
}

// Offset 计算数据库偏移量
func (p *PageQuery) Offset() int {
	if p.Page < 1 {
		p.Page = 1
	}
	return (p.Page - 1) * p.Limit()
}

// Limit 计算每页条数
func (p *PageQuery) Limit() int {
	if p.PageSize < 1 {
		p.PageSize = 10
	}
	if p.PageSize > 100 {
		p.PageSize = 100
	}
	return p.PageSize
}
```

- [ ] **Step 5: 写 `common/vo/pagination.go`**

```go
package vo

// PageResult 通用分页返回结构
type PageResult[T any] struct {
	Total    int64 `json:"total"`
	List     []T   `json:"list"`
	Page     int   `json:"page"`
	PageSize int   `json:"page_size"`
	Pages    int   `json:"pages"`
}

// NewPageResult 创建分页结果
func NewPageResult[T any](total int64, list []T, page, pageSize int) *PageResult[T] {
	pages := int(total) / pageSize
	if int(total)%pageSize > 0 {
		pages++
	}
	if pages < 1 {
		pages = 1
	}
	return &PageResult[T]{
		Total:    total,
		List:     list,
		Page:     page,
		PageSize: pageSize,
		Pages:    pages,
	}
}
```

- [ ] **Step 6: 运行测试确认通过**

```bash
export PATH="/Users/allen/projects/gosdk/go1.25.8/bin:$PATH"
export GOPATH=/tmp/gopath GOMODCACHE=/tmp/gopath/pkg/mod GOFLAGS=-mod=mod
export GOPRIVATE="github.com/PycMono/*"
cd /Users/allen/projects/work/github/FastRAG
gofmt -l common/ && go test -v ./common/dto/... ./common/vo/... && go build ./...
```

Expected: `gofmt -l` 无输出；7 个测试全部 PASS（vo 4 个 + dto 3 个）；`go build` 退出码 0。

- [ ] **Step 7: 提交**

```bash
git add common/dto/ common/vo/
git commit -m "feat: 新增通用分页 DTO/VO"
```

---

### Task 4: 领域层（实体、仓储接口、ID 接口）

**Files:**
- Create: `domain/entity/knowledge/knowledge_base.go`
- Create: `domain/repository/knowledge/knowledge_base.go`
- Create: `domain/repository/knowledge/retriever.go`
- Create: `domain/repository/id_service.go`
- Create: `domain/register.go`
- Create: `domain/service/register.go`

**Interfaces:**
- Consumes: 无（本层禁止 import gin/gorm/redis/http）
- Consumes: 无
- Produces:
  - `entity/knowledge.KnowledgeBase`（字段 `ID,UserID,Name,Description,EmbeddingModel string`、`DocCount int`、`Status int8`、`CreatedAt,UpdatedAt int64`），常量 `entity/knowledge.StatusDisabled int8 = 0`、`StatusEnabled int8 = 1`，方法 `TableName() string`
  - `repository/knowledge.IKnowledgeBaseRepo`（见下）
  - `repository/knowledge.RetrieveQuery{KnowledgeBaseID string; Query string; TopK int}`、`RetrievedChunk{DocID string; ChunkIndex int; Content string; Score float64}`、`IKnowledgeRetriever`
  - `repository.IIDService{NextID() string; NextIntID() int64}`
  - `domain.Register`、`service.Register`（均为 `fx.Options`）

- [ ] **Step 1: 写 `domain/entity/knowledge/knowledge_base.go`**

```go
package knowledge

// 知识库状态
const (
	StatusDisabled int8 = 0 // 停用
	StatusEnabled  int8 = 1 // 启用
)

// KnowledgeBase 知识库实体
// 一个知识库归属于一个用户，后续的文档、切片、向量都以知识库为聚合根
type KnowledgeBase struct {
	ID             string `gorm:"primaryKey;size:32"`               // 雪花 ID
	UserID         string `gorm:"index;size:32;not null"`           // 归属用户
	Name           string `gorm:"size:128;not null"`                // 知识库名称
	Description    string `gorm:"size:512;default:''"`              // 描述
	EmbeddingModel string `gorm:"size:64;default:''"`               // 预留：embedding 模型标识
	DocCount       int    `gorm:"default:0"`                        // 预留：文档数统计
	Status         int8   `gorm:"default:1"`                        // 1=启用 0=停用
	CreatedAt      int64  `gorm:"column:created_at;autoCreateTime:milli"`
	UpdatedAt      int64  `gorm:"column:updated_at;autoUpdateTime:milli"`
}

// TableName 返回表名
func (KnowledgeBase) TableName() string {
	return "knowledge_bases"
}
```

- [ ] **Step 2: 写 `domain/repository/knowledge/knowledge_base.go`**

```go
package knowledge

import (
	"context"

	"github.com/PycMono/FastRAG/domain/entity/knowledge"
)

// IKnowledgeBaseRepo 知识库仓储接口
// 除 Create 外，所有按 ID 的操作都必须同时带上 userID 条件，避免越权访问
type IKnowledgeBaseRepo interface {
	Create(ctx context.Context, kb *knowledge.KnowledgeBase) error
	GetByIDAndUserID(ctx context.Context, id, userID string) (*knowledge.KnowledgeBase, error)
	ListByUserID(ctx context.Context, userID string, page, pageSize int) (total int64, list []*knowledge.KnowledgeBase, err error)
	Update(ctx context.Context, kb *knowledge.KnowledgeBase) error
	DeleteByIDAndUserID(ctx context.Context, id, userID string) error
}
```

- [ ] **Step 3: 写 `domain/repository/knowledge/retriever.go`**

```go
package knowledge

import "context"

// RetrieveQuery 检索请求
type RetrieveQuery struct {
	KnowledgeBaseID string // 目标知识库
	Query           string // 检索语句
	TopK            int    // 返回条数
}

// RetrievedChunk 检索结果片段
type RetrievedChunk struct {
	DocID      string  // 所属文档 ID
	ChunkIndex int     // 文档内切片序号
	Content    string  // 切片正文
	Score      float64 // 相似度得分
}

// IKnowledgeRetriever 知识库检索能力
// 骨架阶段由 infrastructure/persistence/knowledge 的占位实现提供；
// 接入向量库时新增一个实现并在 persistence.Register 中替换即可，其余各层无需改动。
type IKnowledgeRetriever interface {
	Retrieve(ctx context.Context, q RetrieveQuery) ([]*RetrievedChunk, error)
}
```

- [ ] **Step 4: 写 `domain/repository/id_service.go`**

```go
package repository

// IIDService ID 生成服务接口
type IIDService interface {
	NextID() string
	NextIntID() int64
}
```

- [ ] **Step 5: 写 `domain/service/register.go`**

```go
package service

import (
	"go.uber.org/fx"
)

// Register 注册所有领域层服务
var Register = fx.Options(
	// 领域服务为纯逻辑，无需 fx.Provide（由应用服务直接调用）
	// 若未来领域服务需要依赖注入，在此注册
)
```

- [ ] **Step 6: 写 `domain/register.go`**

```go
package domain

import (
	"github.com/PycMono/FastRAG/domain/service"
	"go.uber.org/fx"
)

// Register 注册所有领域层服务
var Register = fx.Options(
	service.Register,
)
```

- [ ] **Step 7: 验证编译与分层红线**

```bash
export PATH="/Users/allen/projects/gosdk/go1.25.8/bin:$PATH"
export GOPATH=/tmp/gopath GOMODCACHE=/tmp/gopath/pkg/mod GOFLAGS=-mod=mod
export GOPRIVATE="github.com/PycMono/*"
cd /Users/allen/projects/work/github/FastRAG
gofmt -l domain/ && go build ./...
grep -rn "gin-gonic/gin\|gorm.io/gorm\|go-redis/v9\|net/http" domain/ || echo "domain layer clean"
```

Expected: `gofmt -l` 无输出；`go build` 退出码 0；grep 无命中，打印 `domain layer clean`。

- [ ] **Step 8: 提交**

```bash
git add domain/
git commit -m "feat: 新增领域层实体、仓储接口与 ID 服务接口"
```

---

### Task 5: 中间件（限流、访问日志）

**Files:**
- Create: `infrastructure/middleware/rate_limit.go`
- Create: `infrastructure/middleware/access_log.go`
- Test: `infrastructure/middleware/middleware_test.go`

**Interfaces:**
- Consumes: `logsdk`（go-logger-sdk v1.0.5）
- Produces: `middleware.RateLimiter`、`middleware.NewRateLimiter(max int, window time.Duration) *RateLimiter`、`(*RateLimiter).Allow(ip string) bool`、`middleware.RateLimit() gin.HandlerFunc`、`middleware.AccessLog() gin.HandlerFunc`

- [ ] **Step 1: 写失败测试 `infrastructure/middleware/middleware_test.go`**

```go
package middleware

import (
	"testing"
	"time"
)

func TestRateLimiter_AllowsUpToLimit(t *testing.T) {
	rl := NewRateLimiter(3, time.Minute)

	for i := 0; i < 3; i++ {
		if !rl.Allow("1.2.3.4") {
			t.Fatalf("request %d should be allowed", i+1)
		}
	}
	if rl.Allow("1.2.3.4") {
		t.Fatal("4th request should be denied")
	}
}

func TestRateLimiter_IsolatesByIP(t *testing.T) {
	rl := NewRateLimiter(1, time.Minute)

	if !rl.Allow("1.2.3.4") {
		t.Fatal("first request from ip A should be allowed")
	}
	if rl.Allow("1.2.3.4") {
		t.Fatal("second request from ip A should be denied")
	}
	if !rl.Allow("5.6.7.8") {
		t.Fatal("first request from ip B should be allowed")
	}
}

func TestRateLimiter_ResetsAfterWindow(t *testing.T) {
	rl := NewRateLimiter(1, 20*time.Millisecond)

	if !rl.Allow("1.2.3.4") {
		t.Fatal("first request should be allowed")
	}
	if rl.Allow("1.2.3.4") {
		t.Fatal("second request should be denied before window expires")
	}

	time.Sleep(40 * time.Millisecond)

	if !rl.Allow("1.2.3.4") {
		t.Fatal("request after window expiry should be allowed again")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
export PATH="/Users/allen/projects/gosdk/go1.25.8/bin:$PATH"
export GOPATH=/tmp/gopath GOMODCACHE=/tmp/gopath/pkg/mod GOFLAGS=-mod=mod
export GOPRIVATE="github.com/PycMono/*"
cd /Users/allen/projects/work/github/FastRAG
go test ./infrastructure/middleware/...
```

Expected: FAIL —— `undefined: NewRateLimiter`。

- [ ] **Step 3: 写 `infrastructure/middleware/rate_limit.go`**

```go
package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// rateLimitEntry 单个 IP 的请求记录
type rateLimitEntry struct {
	count   int
	resetAt time.Time
}

// RateLimiter IP 级频率限制器
type RateLimiter struct {
	mu      sync.Mutex
	entries map[string]*rateLimitEntry
	max     int
	window  time.Duration
}

// NewRateLimiter 创建频率限制器
//   - max: 窗口内允许的最大请求数
//   - window: 计数窗口
func NewRateLimiter(max int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		entries: make(map[string]*rateLimitEntry),
		max:     max,
		window:  window,
	}

	// 定期清理过期条目，避免 map 无限增长
	go func() {
		ticker := time.NewTicker(window)
		defer ticker.Stop()
		for range ticker.C {
			rl.mu.Lock()
			now := time.Now()
			for key, e := range rl.entries {
				if now.After(e.resetAt) {
					delete(rl.entries, key)
				}
			}
			rl.mu.Unlock()
		}
	}()

	return rl
}

// Allow 检查 IP 是否允许通过
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	e, ok := rl.entries[ip]
	if !ok || now.After(e.resetAt) {
		rl.entries[ip] = &rateLimitEntry{
			count:   1,
			resetAt: now.Add(rl.window),
		}
		return true
	}

	if e.count >= rl.max {
		return false
	}

	e.count++
	return true
}

// RateLimit 返回 gin 频率限制中间件
// 默认：600 请求 / 分钟
func RateLimit() gin.HandlerFunc {
	limiter := NewRateLimiter(600, time.Minute)

	return func(c *gin.Context) {
		if !limiter.Allow(c.ClientIP()) {
			c.AbortWithStatus(http.StatusTooManyRequests)
			return
		}
		c.Next()
	}
}
```

- [ ] **Step 4: 写 `infrastructure/middleware/access_log.go`**

```go
package middleware

import (
	"time"

	logsdk "github.com/PycMono/go-logger-sdk"
	"github.com/gin-gonic/gin"
)

// AccessLog 访问日志中间件
// 记录 method / path / status / latency_ms / client_ip
func AccessLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()

		c.Next()

		logsdk.Info(c.Request.Context(), "access",
			logsdk.Any("method", c.Request.Method),
			logsdk.Any("path", c.Request.URL.Path),
			logsdk.Any("status", c.Writer.Status()),
			logsdk.Any("latency_ms", time.Since(start).Milliseconds()),
			logsdk.Any("client_ip", c.ClientIP()),
		)
	}
}
```

- [ ] **Step 5: 运行测试确认通过**

```bash
export PATH="/Users/allen/projects/gosdk/go1.25.8/bin:$PATH"
export GOPATH=/tmp/gopath GOMODCACHE=/tmp/gopath/pkg/mod GOFLAGS=-mod=mod
export GOPRIVATE="github.com/PycMono/*"
cd /Users/allen/projects/work/github/FastRAG
gofmt -l infrastructure/middleware/ && go test -v -race ./infrastructure/middleware/... && go build ./...
```

Expected: `gofmt -l` 无输出；3 个测试全部 PASS；`go build` 退出码 0。

- [ ] **Step 6: 提交**

```bash
git add infrastructure/middleware/
git commit -m "feat: 新增限流与访问日志中间件"
```

---

### Task 6: 基础设施驱动（MySQL、Redis、Gin 扩展）

**Files:**
- Create: `infrastructure/driver/mysql/mysql.go`
- Create: `infrastructure/driver/redis/redis.go`
- Create: `infrastructure/driver/gingext/response.go`
- Create: `infrastructure/driver/gingext/gingext.go`

**Interfaces:**
- Consumes: `config.*`（Task 1）、`common/errors`（Task 2）、`infrastructure/middleware`（Task 5）
- Produces:
  - `sqlsdk.Provider`（`github.com/PycMono/go-mysql-sdk`）、`mysql.NewProvider(conf *config.Config) (sqlsdk.Provider, error)`
  - `redis.NewClient(conf *config.Config) (goredis.UniversalClient, error)` —— addr 为空时返回 `(nil, nil)`
  - `gingext.Send(c *gin.Context, data interface{}, err error)`
  - `gingext.NewEngine(conf *config.Config) *gin.Engine`、`gingext.NewHTTPServer(router *gin.Engine, conf *config.Config) *ginsdk.HTTPServer`

- [ ] **Step 1: 写 `infrastructure/driver/mysql/mysql.go`**

```go
package mysql

import (
	sqlsdk "github.com/PycMono/go-mysql-sdk"
	"github.com/PycMono/FastRAG/infrastructure/config"
)

// NewProvider 初始化 MySQL Provider（go-mysql-sdk）
func NewProvider(conf *config.Config) (sqlsdk.Provider, error) {
	opts := &sqlsdk.Options{
		Host:          conf.MySQL.Host,
		Port:          conf.MySQL.Port,
		Database:      conf.MySQL.Database,
		User:          conf.MySQL.User,
		Password:      conf.MySQL.Password,
		MaxOpen:       conf.MySQL.MaxOpen,
		MaxIdle:       conf.MySQL.MaxIdle,
		Lifetime:      conf.MySQL.ConnLifetime,
		Timeout:       conf.MySQL.ConnTimeout,
		LogLevel:      conf.MySQL.LogLevel,
		SlowThreshold: conf.MySQL.SlowThreshold,
	}

	provider := sqlsdk.NewTransProvider(opts)
	return provider, nil
}
```

- [ ] **Step 2: 写 `infrastructure/driver/redis/redis.go`**

```go
package redis

import (
	"context"
	"fmt"

	"github.com/PycMono/FastRAG/infrastructure/config"
	"github.com/PycMono/go-cache-sdk/redis/connect"
	goredis "github.com/redis/go-redis/v9"
)

// NewClient 初始化 Redis 客户端（go-cache-sdk）
// redis.addr 为空表示不启用 Redis，此时返回 nil 客户端而非报错，
// 使骨架在没有 Redis 的环境下也能正常启动。
func NewClient(conf *config.Config) (goredis.UniversalClient, error) {
	if len(conf.Redis.Addr) == 0 || conf.Redis.Addr[0] == "" {
		return nil, nil
	}

	rConf := &connect.Config{
		AppName:    conf.App,
		ClientName: "cache",
		Addr:       conf.Redis.Addr,
		Password:   conf.Redis.Password,
		DB:         conf.Redis.DB,
		PoolSize:   conf.Redis.PoolSize,
	}

	client, err := connect.InitClient(context.Background(), rConf)
	if err != nil {
		return nil, fmt.Errorf("init redis client failed: %w", err)
	}
	return client, nil
}
```

- [ ] **Step 3: 写 `infrastructure/driver/gingext/response.go`**

```go
package gingext

import (
	"errors"
	"net/http"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	ginsdk "github.com/PycMono/go-gin-sdk"
	"github.com/gin-gonic/gin"
)

// Send 统一响应封装
// 参考 go-gin-sdk 的 DefaultSender，支持标准 gin 上下文。
// 用 errors.As 而非类型断言提取业务错误码：Controller 惯用
// fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err) 包装绑定错误，
// 包装后动态类型为 *fmt.wrapError，类型断言会失败。
func Send(c *gin.Context, data interface{}, err error) {
	if data == nil {
		data = struct{}{}
	}

	body := &ginsdk.HTTPJSONBody{
		Code: ginsdk.StatusOK,
		Msg:  "success",
		Data: data,
	}

	if err != nil {
		body.Data = struct{}{}

		// 优先使用 CodeError 的错误码
		var codeErr apperrors.CodeError
		if errors.As(err, &codeErr) {
			body.Code = codeErr.Code()
			body.Msg = codeErr.Message()
			c.JSON(httpStatusForCode(codeErr.Code()), body)
			return
		}

		// 兼容标准 error（fallback）
		body.Code = ginsdk.ErrCodeUnknown
		body.Msg = err.Error()
		c.JSON(http.StatusOK, body)
		return
	}

	c.JSON(http.StatusOK, body)
}

// httpStatusForCode 根据业务错误码映射 HTTP 状态码
func httpStatusForCode(code int) int {
	switch code {
	case apperrors.CodeInvalidParam:
		return http.StatusBadRequest
	case apperrors.CodeUnauthorized:
		return http.StatusUnauthorized
	case apperrors.CodeForbidden:
		return http.StatusForbidden
	case apperrors.CodeNotFound, apperrors.CodeKnowledgeBaseNotFound:
		return http.StatusNotFound
	case apperrors.CodeRateLimited:
		return http.StatusTooManyRequests
	default:
		return http.StatusOK
	}
}
```

- [ ] **Step 4: 写 `infrastructure/driver/gingext/gingext.go`**

```go
package gingext

import (
	"time"

	"github.com/PycMono/FastRAG/infrastructure/config"
	mw "github.com/PycMono/FastRAG/infrastructure/middleware"
	ginsdk "github.com/PycMono/go-gin-sdk"
	"github.com/PycMono/go-gin-sdk/middleware"
	"github.com/gin-gonic/gin"
)

// NewEngine 初始化标准 Gin 引擎
// 引入 go-gin-sdk 的中间件能力（Bizctx、CORS、Tracing、Logger）以及框架级中间件（访问日志、限流）
//
// Bizctx 会把请求头 X-Bizctx-UserID 解析进 bizctx，
// Controller 通过 bizctx.GetUserID(c.Request.Context()) 取用户标识。
func NewEngine(conf *config.Config) *gin.Engine {
	if !conf.Debug {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()

	// 全局中间件（顺序：业务上下文 → 跨域 → 链路 → 框架日志 → 访问日志 → 限流）
	router.Use(middleware.Bizctx())
	router.Use(middleware.CORS())
	router.Use(middleware.Tracing())
	router.Use(middleware.Logger())
	router.Use(mw.AccessLog())
	router.Use(mw.RateLimit())

	// Recovery 放在最后，确保前面中间件里的 panic 也能被捕获
	router.Use(gin.Recovery())

	return router
}

// NewHTTPServer 创建 HTTP 服务器
// 使用 go-gin-sdk 的 HTTPServer 封装，支持优雅关闭和超时配置
func NewHTTPServer(router *gin.Engine, conf *config.Config) *ginsdk.HTTPServer {
	opts := &ginsdk.ServerOptions{
		Host:         conf.HTTP.Host,
		Port:         conf.HTTP.Port,
		ReadTimeout:  time.Duration(conf.HTTP.ReadTimeout) * time.Second,
		WriteTimeout: time.Duration(conf.HTTP.WriteTimeout) * time.Second,
	}

	return ginsdk.NewHTTPServer(router, opts)
}
```

- [ ] **Step 5: 验证编译**

```bash
export PATH="/Users/allen/projects/gosdk/go1.25.8/bin:$PATH"
export GOPATH=/tmp/gopath GOMODCACHE=/tmp/gopath/pkg/mod GOFLAGS=-mod=mod
export GOPRIVATE="github.com/PycMono/*"
cd /Users/allen/projects/work/github/FastRAG
gofmt -l infrastructure/driver/ && go vet ./infrastructure/... && go build ./...
```

Expected: `gofmt -l` 无输出；`go vet` 与 `go build` 退出码 0。

- [ ] **Step 6: 提交**

```bash
git add infrastructure/driver/
git commit -m "feat: 新增 MySQL/Redis 驱动与 Gin 扩展（统一响应、引擎、HTTP Server）"
```

---

### Task 7: 雪花 ID 实现

**Files:**
- Create: `infrastructure/serviceimpl/id_service.go`
- Create: `infrastructure/serviceimpl/register.go`

**Interfaces:**
- Consumes: `repository.IIDService`（Task 4）、`config.Config`（Task 1）
- Produces: `serviceimpl.IDService`、`serviceimpl.NewIDService(workerID int64) (*IDService, error)`、`(*IDService).NextID() string`、`(*IDService).NextIntID() int64`、`serviceimpl.Register`（`fx.Options`，把 `repository.IIDService` 绑定到 `*IDService`）

- [ ] **Step 1: 写 `infrastructure/serviceimpl/id_service.go`**

```go
package serviceimpl

import (
	"fmt"

	"github.com/bwmarrin/snowflake"
)

// IDService ID 服务实现
type IDService struct {
	node *snowflake.Node
}

// NewIDService 创建 ID 服务
//   - workerID: 雪花算法工作节点 ID，范围 0-1023，多实例时必须全局唯一
func NewIDService(workerID int64) (*IDService, error) {
	node, err := snowflake.NewNode(workerID)
	if err != nil {
		return nil, fmt.Errorf("create snowflake node: %w", err)
	}
	return &IDService{node: node}, nil
}

// NextID 生成 string 类型雪花 ID
func (svc *IDService) NextID() string {
	return svc.node.Generate().String()
}

// NextIntID 生成 int64 类型雪花 ID
func (svc *IDService) NextIntID() int64 {
	return svc.node.Generate().Int64()
}
```

- [ ] **Step 2: 写 `infrastructure/serviceimpl/register.go`**

```go
package serviceimpl

import (
	"github.com/PycMono/FastRAG/domain/repository"
	"github.com/PycMono/FastRAG/infrastructure/config"
	"go.uber.org/fx"
)

// Register 注册 serviceimpl 层组件
var Register = fx.Options(
	fx.Provide(func(conf *config.Config) (repository.IIDService, error) {
		svc, err := NewIDService(int64(conf.SnowflakeNodeID))
		if err != nil {
			return nil, err
		}
		return svc, nil
	}),
)
```

- [ ] **Step 3: 验证编译**

```bash
export PATH="/Users/allen/projects/gosdk/go1.25.8/bin:$PATH"
export GOPATH=/tmp/gopath GOMODCACHE=/tmp/gopath/pkg/mod GOFLAGS=-mod=mod
export GOPRIVATE="github.com/PycMono/*"
cd /Users/allen/projects/work/github/FastRAG
gofmt -l infrastructure/serviceimpl/ && go build ./...
```

Expected: 无输出，退出码 0。

- [ ] **Step 4: 提交**

```bash
git add infrastructure/serviceimpl/
git commit -m "feat: 新增雪花 ID 生成服务"
```

---

### Task 8: 知识库持久化实现

**Files:**
- Create: `infrastructure/persistence/knowledge/knowledge_base_repo.go`
- Create: `infrastructure/persistence/knowledge/retriever_stub.go`
- Create: `infrastructure/persistence/register.go`

**Interfaces:**
- Consumes: `entity/knowledge`、`repository/knowledge`、`repository.IIDService`（Task 4）、`common/errors`（Task 2）、`sqlsdk.Provider`（Task 6）
- Produces:
  - `persistence/knowledge.KnowledgeBaseRepo`、`persistence/knowledge.NewKnowledgeBaseRepo(provider sqlsdk.Provider, idService repository.IIDService) knowledgerepo.IKnowledgeBaseRepo`
  - `persistence/knowledge.RetrieverStub`、`persistence/knowledge.NewRetrieverStub() knowledgerepo.IKnowledgeRetriever`
  - `persistence.Register`（`fx.Options`）

- [ ] **Step 1: 写 `infrastructure/persistence/knowledge/knowledge_base_repo.go`**

```go
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
```

- [ ] **Step 2: 写 `infrastructure/persistence/knowledge/retriever_stub.go`**

```go
package knowledge

import (
	"context"

	apperrors "github.com/PycMono/FastRAG/common/errors"
	knowledgerepo "github.com/PycMono/FastRAG/domain/repository/knowledge"
)

// RetrieverStub 检索能力占位实现
// 骨架阶段尚未接入向量库，统一返回「检索未实现」错误码。
// 接入向量库时：新增一个实现 knowledgerepo.IKnowledgeRetriever 的类型，
// 在 infrastructure/persistence/register.go 中替换本实现即可，其余各层无需改动。
type RetrieverStub struct{}

// NewRetrieverStub 创建检索占位实现
func NewRetrieverStub() knowledgerepo.IKnowledgeRetriever {
	return &RetrieverStub{}
}

// Retrieve 返回未实现错误
func (s *RetrieverStub) Retrieve(ctx context.Context, q knowledgerepo.RetrieveQuery) ([]*knowledgerepo.RetrievedChunk, error) {
	return nil, apperrors.ErrKnowledgeRetrievalNotImpl
}
```

- [ ] **Step 3: 写 `infrastructure/persistence/register.go`**

```go
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
```

- [ ] **Step 4: 验证编译**

```bash
export PATH="/Users/allen/projects/gosdk/go1.25.8/bin:$PATH"
export GOPATH=/tmp/gopath GOMODCACHE=/tmp/gopath/pkg/mod GOFLAGS=-mod=mod
export GOPRIVATE="github.com/PycMono/*"
cd /Users/allen/projects/work/github/FastRAG
gofmt -l infrastructure/persistence/ && go vet ./infrastructure/persistence/... && go build ./...
```

Expected: 无输出，退出码 0。

- [ ] **Step 5: 提交**

```bash
git add infrastructure/persistence/
git commit -m "feat: 新增知识库 MySQL 仓储实现与检索占位实现"
```

---

### Task 9: 知识库应用服务

**Files:**
- Create: `common/dto/knowledge.go`
- Create: `common/vo/knowledge.go`
- Create: `application/service/knowledge/service.go`
- Create: `application/service/register.go`
- Test: `application/service/knowledge/service_test.go`

**Interfaces:**
- Consumes: `dto.PageQuery` / `vo.PageResult`（Task 3）、`entity/knowledge` / `repository/knowledge`（Task 4）、`common/errors`（Task 2）
- Produces:
  - `dto.CreateKnowledgeBaseDTO{Name string; Description string; EmbeddingModel string}`
  - `dto.UpdateKnowledgeBaseDTO{Name *string; Description *string; EmbeddingModel *string; Status *int8}`
  - `dto.ListKnowledgeBaseQuery`（内嵌 `PageQuery`）
  - `dto.SearchKnowledgeBaseDTO{Query string; TopK int}`
  - `vo.KnowledgeBaseVO`、`vo.KnowledgeBaseListVO = PageResult[*KnowledgeBaseVO]`、`vo.RetrievedChunkVO`
  - `knowledge.Service`、`knowledge.NewService(repo knowledgerepo.IKnowledgeBaseRepo, retriever knowledgerepo.IKnowledgeRetriever, idService repository.IIDService) *Service`
  - 方法 `Create(ctx, userID string, param dto.CreateKnowledgeBaseDTO) (*vo.KnowledgeBaseVO, error)`、`Get(ctx, userID, id string) (*vo.KnowledgeBaseVO, error)`、`List(ctx, userID string, query dto.ListKnowledgeBaseQuery) (*vo.KnowledgeBaseListVO, error)`、`Update(ctx, userID, id string, param dto.UpdateKnowledgeBaseDTO) error`、`Delete(ctx, userID, id string) error`、`Search(ctx, userID, id string, param dto.SearchKnowledgeBaseDTO) ([]*vo.RetrievedChunkVO, error)`
  - `service.Register`

- [ ] **Step 1: 写 `common/dto/knowledge.go`**

```go
package dto

// CreateKnowledgeBaseDTO 创建知识库请求
type CreateKnowledgeBaseDTO struct {
	Name           string `json:"name" binding:"required,max=128"`
	Description    string `json:"description" binding:"max=512"`
	EmbeddingModel string `json:"embedding_model" binding:"max=64"`
}

// UpdateKnowledgeBaseDTO 更新知识库请求
// 字段为指针：nil 表示本次不更新该字段
type UpdateKnowledgeBaseDTO struct {
	Name           *string `json:"name" binding:"omitempty,max=128"`
	Description    *string `json:"description" binding:"omitempty,max=512"`
	EmbeddingModel *string `json:"embedding_model" binding:"omitempty,max=64"`
	Status         *int8   `json:"status" binding:"omitempty,oneof=0 1"`
}

// ListKnowledgeBaseQuery 知识库列表查询参数
type ListKnowledgeBaseQuery struct {
	PageQuery
}

// SearchKnowledgeBaseDTO 知识库检索请求
type SearchKnowledgeBaseDTO struct {
	Query string `json:"query" binding:"required"`
	TopK  int    `json:"top_k" binding:"omitempty,gte=1,lte=50"`
}
```

- [ ] **Step 2: 写 `common/vo/knowledge.go`**

```go
package vo

// KnowledgeBaseVO 知识库响应
type KnowledgeBaseVO struct {
	ID             string `json:"id"`
	UserID         string `json:"user_id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	EmbeddingModel string `json:"embedding_model"`
	DocCount       int    `json:"doc_count"`
	Status         int8   `json:"status"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}

// KnowledgeBaseListVO 知识库列表响应
type KnowledgeBaseListVO = PageResult[*KnowledgeBaseVO]

// RetrievedChunkVO 检索结果片段
type RetrievedChunkVO struct {
	DocID      string  `json:"doc_id"`
	ChunkIndex int     `json:"chunk_index"`
	Content    string  `json:"content"`
	Score      float64 `json:"score"`
}
```

- [ ] **Step 3: 写失败测试 `application/service/knowledge/service_test.go`**

```go
package knowledge

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	knowledgeentity "github.com/PycMono/FastRAG/domain/entity/knowledge"
	knowledgerepo "github.com/PycMono/FastRAG/domain/repository/knowledge"
)

// ─── 测试替身 ────────────────────────────────────────────────────────────────

// fakeRepo 内存仓储
type fakeRepo struct {
	items     map[string]*knowledgeentity.KnowledgeBase
	createErr error
	updateErr error
	deleteErr error
	listErr   error
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{items: make(map[string]*knowledgeentity.KnowledgeBase)}
}

func (r *fakeRepo) Create(ctx context.Context, kb *knowledgeentity.KnowledgeBase) error {
	if r.createErr != nil {
		return r.createErr
	}
	cp := *kb
	r.items[kb.ID] = &cp
	return nil
}

func (r *fakeRepo) GetByIDAndUserID(ctx context.Context, id, userID string) (*knowledgeentity.KnowledgeBase, error) {
	kb, ok := r.items[id]
	if !ok || kb.UserID != userID {
		return nil, apperrors.ErrKnowledgeBaseNotFound
	}
	cp := *kb
	return &cp, nil
}

func (r *fakeRepo) ListByUserID(ctx context.Context, userID string, page, pageSize int) (int64, []*knowledgeentity.KnowledgeBase, error) {
	if r.listErr != nil {
		return 0, nil, r.listErr
	}
	var all []*knowledgeentity.KnowledgeBase
	for _, kb := range r.items {
		if kb.UserID == userID {
			cp := *kb
			all = append(all, &cp)
		}
	}
	total := int64(len(all))
	start := (page - 1) * pageSize
	if start < 0 {
		start = 0
	}
	if start > len(all) {
		start = len(all)
	}
	end := start + pageSize
	if end > len(all) {
		end = len(all)
	}
	return total, all[start:end], nil
}

func (r *fakeRepo) Update(ctx context.Context, kb *knowledgeentity.KnowledgeBase) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	cp := *kb
	r.items[kb.ID] = &cp
	return nil
}

func (r *fakeRepo) DeleteByIDAndUserID(ctx context.Context, id, userID string) error {
	if r.deleteErr != nil {
		return r.deleteErr
	}
	kb, ok := r.items[id]
	if !ok || kb.UserID != userID {
		return nil
	}
	delete(r.items, id)
	return nil
}

// fakeRetriever 检索替身
type fakeRetriever struct {
	chunks []*knowledgerepo.RetrievedChunk
	err    error
}

func (f *fakeRetriever) Retrieve(ctx context.Context, q knowledgerepo.RetrieveQuery) ([]*knowledgerepo.RetrievedChunk, error) {
	return f.chunks, f.err
}

// fakeIDService 自增 ID 生成器
type fakeIDService struct{ n int }

func (f *fakeIDService) NextID() string { f.n++; return "id-" + strconv.Itoa(f.n) }

func (f *fakeIDService) NextIntID() int64 { f.n++; return int64(f.n) }

func newTestService(repo knowledgerepo.IKnowledgeBaseRepo, retriever knowledgerepo.IKnowledgeRetriever) *Service {
	return NewService(repo, retriever, &fakeIDService{})
}

// ─── Create ─────────────────────────────────────────────────────────────────

func TestService_Create(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, &fakeRetriever{})

	got, err := svc.Create(context.Background(), "u1", dto.CreateKnowledgeBaseDTO{Name: "kb-a", Description: "d"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if got.ID == "" {
		t.Fatal("Create() should assign an ID")
	}
	if got.UserID != "u1" {
		t.Fatalf("UserID = %q, want %q", got.UserID, "u1")
	}
	if got.Status != knowledgeentity.StatusEnabled {
		t.Fatalf("Status = %d, want %d", got.Status, knowledgeentity.StatusEnabled)
	}
	if _, ok := repo.items[got.ID]; !ok {
		t.Fatal("Create() should persist the record")
	}
}

func TestService_Create_WrapsRepoError(t *testing.T) {
	repo := newFakeRepo()
	repo.createErr = errors.New("connection refused")
	svc := newTestService(repo, &fakeRetriever{})

	_, err := svc.Create(context.Background(), "u1", dto.CreateKnowledgeBaseDTO{Name: "kb-a"})

	sys, ok := apperrors.AsSysError(err)
	if !ok {
		t.Fatalf("expected a SysError, got %v", err)
	}
	if sys.Code() != apperrors.CodeKnowledgeBaseCreateFail {
		t.Fatalf("Code() = %d, want %d", sys.Code(), apperrors.CodeKnowledgeBaseCreateFail)
	}
}

// ─── Get ────────────────────────────────────────────────────────────────────

func TestService_Get_NotFound(t *testing.T) {
	svc := newTestService(newFakeRepo(), &fakeRetriever{})

	_, err := svc.Get(context.Background(), "u1", "missing")

	if !errors.Is(err, apperrors.ErrKnowledgeBaseNotFound) {
		t.Fatalf("Get() error = %v, want ErrKnowledgeBaseNotFound", err)
	}
}

func TestService_Get_OtherUserCannotRead(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, &fakeRetriever{})

	created, err := svc.Create(context.Background(), "u1", dto.CreateKnowledgeBaseDTO{Name: "kb-a"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err = svc.Get(context.Background(), "u2", created.ID)

	if !errors.Is(err, apperrors.ErrKnowledgeBaseNotFound) {
		t.Fatalf("Get() by another user error = %v, want ErrKnowledgeBaseNotFound", err)
	}
}

// ─── List ───────────────────────────────────────────────────────────────────

func TestService_List_Paginates(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, &fakeRetriever{})

	for _, name := range []string{"a", "b", "c"} {
		if _, err := svc.Create(context.Background(), "u1", dto.CreateKnowledgeBaseDTO{Name: name}); err != nil {
			t.Fatalf("Create(%s) error = %v", name, err)
		}
	}

	got, err := svc.List(context.Background(), "u1", dto.ListKnowledgeBaseQuery{
		PageQuery: dto.PageQuery{Page: 1, PageSize: 2},
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if got.Total != 3 {
		t.Fatalf("Total = %d, want 3", got.Total)
	}
	if len(got.List) != 2 {
		t.Fatalf("len(List) = %d, want 2", len(got.List))
	}
	if got.Pages != 2 {
		t.Fatalf("Pages = %d, want 2", got.Pages)
	}
}

func TestService_List_NormalizesInvalidPaging(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, &fakeRetriever{})

	got, err := svc.List(context.Background(), "u1", dto.ListKnowledgeBaseQuery{
		PageQuery: dto.PageQuery{Page: 0, PageSize: 0},
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if got.Page != 1 || got.PageSize != 10 {
		t.Fatalf("Page/PageSize = %d/%d, want 1/10", got.Page, got.PageSize)
	}
}

// ─── Update ─────────────────────────────────────────────────────────────────

func TestService_Update_OnlyProvidedFields(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, &fakeRetriever{})

	created, err := svc.Create(context.Background(), "u1", dto.CreateKnowledgeBaseDTO{
		Name: "old", Description: "keep-me", EmbeddingModel: "text-embedding-3",
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	newName := "new"
	if err := svc.Update(context.Background(), "u1", created.ID, dto.UpdateKnowledgeBaseDTO{Name: &newName}); err != nil {
		t.Fatalf("Update() error = %v", err)
	}

	got, err := svc.Get(context.Background(), "u1", created.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Name != "new" {
		t.Fatalf("Name = %q, want %q", got.Name, "new")
	}
	if got.Description != "keep-me" {
		t.Fatalf("Description = %q, want %q (unset field must be preserved)", got.Description, "keep-me")
	}
	if got.EmbeddingModel != "text-embedding-3" {
		t.Fatalf("EmbeddingModel = %q, want it preserved", got.EmbeddingModel)
	}
}

func TestService_Update_NotFound(t *testing.T) {
	svc := newTestService(newFakeRepo(), &fakeRetriever{})

	name := "x"
	err := svc.Update(context.Background(), "u1", "missing", dto.UpdateKnowledgeBaseDTO{Name: &name})

	if !errors.Is(err, apperrors.ErrKnowledgeBaseNotFound) {
		t.Fatalf("Update() error = %v, want ErrKnowledgeBaseNotFound", err)
	}
}

func TestService_Update_WrapsRepoError(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, &fakeRetriever{})

	created, err := svc.Create(context.Background(), "u1", dto.CreateKnowledgeBaseDTO{Name: "kb-a"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	repo.updateErr = errors.New("deadlock")

	name := "x"
	err = svc.Update(context.Background(), "u1", created.ID, dto.UpdateKnowledgeBaseDTO{Name: &name})

	sys, ok := apperrors.AsSysError(err)
	if !ok {
		t.Fatalf("expected a SysError, got %v", err)
	}
	if sys.Code() != apperrors.CodeKnowledgeBaseUpdateFail {
		t.Fatalf("Code() = %d, want %d", sys.Code(), apperrors.CodeKnowledgeBaseUpdateFail)
	}
}

// ─── Delete ─────────────────────────────────────────────────────────────────

func TestService_Delete(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, &fakeRetriever{})

	created, err := svc.Create(context.Background(), "u1", dto.CreateKnowledgeBaseDTO{Name: "kb-a"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	if err := svc.Delete(context.Background(), "u1", created.ID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, ok := repo.items[created.ID]; ok {
		t.Fatal("Delete() should remove the record")
	}
}

func TestService_Delete_WrapsRepoError(t *testing.T) {
	repo := newFakeRepo()
	repo.deleteErr = errors.New("lock wait timeout")
	svc := newTestService(repo, &fakeRetriever{})

	err := svc.Delete(context.Background(), "u1", "anything")

	sys, ok := apperrors.AsSysError(err)
	if !ok {
		t.Fatalf("expected a SysError, got %v", err)
	}
	if sys.Code() != apperrors.CodeKnowledgeBaseDeleteFail {
		t.Fatalf("Code() = %d, want %d", sys.Code(), apperrors.CodeKnowledgeBaseDeleteFail)
	}
}

// ─── Search（检索扩展点）─────────────────────────────────────────────────────

func TestService_Search_ReturnsNotImplementedFromStub(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, &fakeRetriever{err: apperrors.ErrKnowledgeRetrievalNotImpl})

	created, err := svc.Create(context.Background(), "u1", dto.CreateKnowledgeBaseDTO{Name: "kb-a"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	_, err = svc.Search(context.Background(), "u1", created.ID, dto.SearchKnowledgeBaseDTO{Query: "hello"})

	if !errors.Is(err, apperrors.ErrKnowledgeRetrievalNotImpl) {
		t.Fatalf("Search() error = %v, want ErrKnowledgeRetrievalNotImpl", err)
	}
}

func TestService_Search_RejectsUnknownKnowledgeBase(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, &fakeRetriever{
		chunks: []*knowledgerepo.RetrievedChunk{{DocID: "d1", Content: "c"}},
	})

	_, err := svc.Search(context.Background(), "u1", "missing", dto.SearchKnowledgeBaseDTO{Query: "hello"})

	if !errors.Is(err, apperrors.ErrKnowledgeBaseNotFound) {
		t.Fatalf("Search() error = %v, want ErrKnowledgeBaseNotFound", err)
	}
}

func TestService_Search_MapsChunksToVO(t *testing.T) {
	repo := newFakeRepo()
	svc := newTestService(repo, &fakeRetriever{
		chunks: []*knowledgerepo.RetrievedChunk{{DocID: "d1", ChunkIndex: 2, Content: "body", Score: 0.87}},
	})

	created, err := svc.Create(context.Background(), "u1", dto.CreateKnowledgeBaseDTO{Name: "kb-a"})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	got, err := svc.Search(context.Background(), "u1", created.ID, dto.SearchKnowledgeBaseDTO{Query: "hello"})
	if err != nil {
		t.Fatalf("Search() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(result) = %d, want 1", len(got))
	}
	if got[0].DocID != "d1" || got[0].ChunkIndex != 2 || got[0].Content != "body" || got[0].Score != 0.87 {
		t.Fatalf("unexpected chunk mapping: %+v", got[0])
	}
}
```

- [ ] **Step 4: 运行测试确认失败**

```bash
export PATH="/Users/allen/projects/gosdk/go1.25.8/bin:$PATH"
export GOPATH=/tmp/gopath GOMODCACHE=/tmp/gopath/pkg/mod GOFLAGS=-mod=mod
export GOPRIVATE="github.com/PycMono/*"
cd /Users/allen/projects/work/github/FastRAG
go test ./application/service/knowledge/...
```

Expected: FAIL —— `undefined: NewService`、`undefined: Service`。

- [ ] **Step 5: 写 `application/service/knowledge/service.go`**

```go
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
```

- [ ] **Step 6: 写 `application/service/register.go`**

```go
package service

import (
	"github.com/PycMono/FastRAG/application/service/knowledge"
	"go.uber.org/fx"
)

// Register 注册所有应用层服务
var Register = fx.Options(
	fx.Provide(knowledge.NewService),
)
```

- [ ] **Step 7: 运行测试确认通过**

```bash
export PATH="/Users/allen/projects/gosdk/go1.25.8/bin:$PATH"
export GOPATH=/tmp/gopath GOMODCACHE=/tmp/gopath/pkg/mod GOFLAGS=-mod=mod
export GOPRIVATE="github.com/PycMono/*"
cd /Users/allen/projects/work/github/FastRAG
gofmt -l application/ common/ && go test -v -race ./application/... && go build ./...
```

Expected: `gofmt -l` 无输出；14 个测试全部 PASS；`go build` 退出码 0。

- [ ] **Step 8: 提交**

```bash
git add common/dto/knowledge.go common/vo/knowledge.go application/
git commit -m "feat: 新增知识库应用服务与 DTO/VO"
```

---

### Task 10: HTTP 控制器与路由

**Files:**
- Create: `infrastructure/controller/http/health/controller.go`
- Create: `infrastructure/controller/http/knowledge/controller.go`
- Create: `infrastructure/controller/http/register.go`
- Create: `infrastructure/controller/register.go`

**Interfaces:**
- Consumes: `config.Config`（Task 1）、`apperrors`（Task 2）、`dto`/`vo`（Task 9）、`knowledge.Service`（Task 9）、`gingext.Send`（Task 6）、`bizctx`（go-context-sdk）、`sqlsdk.Provider`（Task 6）、`goredis.UniversalClient`（Task 6）
- Produces:
  - `health.Controller`、`health.NewController(conf *config.Config, provider sqlsdk.Provider, redisClient goredis.UniversalClient) *Controller`，方法 `Health(c *gin.Context)`、`Ready(c *gin.Context)`
  - `knowledge.Controller`、`knowledge.NewController(service *appsvc.Service) *Controller`，方法 `Create`、`List`、`Get`、`Update`、`Delete`、`Search`（均为 `func(*gin.Context)`）
  - 包 `http` 的 `Register`、`RouteDeps`、`RegisterRoutes(d RouteDeps)`
  - `controller.Register`

**路由表：**

| 方法 | 路径 |
|------|------|
| GET | `/health` |
| GET | `/ready` |
| POST | `/api/v1/knowledge-bases` |
| GET | `/api/v1/knowledge-bases` |
| GET | `/api/v1/knowledge-bases/:id` |
| PUT | `/api/v1/knowledge-bases/:id` |
| DELETE | `/api/v1/knowledge-bases/:id` |
| POST | `/api/v1/knowledge-bases/:id/search` |

- [ ] **Step 1: 写 `infrastructure/controller/http/health/controller.go`**

```go
package health

import (
	"context"
	"net/http"
	"time"

	"github.com/PycMono/FastRAG/infrastructure/config"
	sqlsdk "github.com/PycMono/go-mysql-sdk"
	"github.com/gin-gonic/gin"
	goredis "github.com/redis/go-redis/v9"
)

// Redis 未启用时 config.Redis.Addr 为空
const redisDisabled = "disabled"

// Controller 健康检查控制器
type Controller struct {
	conf     *config.Config
	provider sqlsdk.Provider
	redis    goredis.UniversalClient
}

// NewController 创建健康检查控制器
func NewController(conf *config.Config, provider sqlsdk.Provider, redisClient goredis.UniversalClient) *Controller {
	return &Controller{conf: conf, provider: provider, redis: redisClient}
}

// Health 存活探针 —— 服务是否正在运行
// 注意：本方法不走统一响应封装（gingext.Send），K8s 探针需要固定格式
func (ctl *Controller) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "up",
		"timestamp": time.Now().Format(time.RFC3339),
	})
}

// Ready 就绪探针 —— 依赖是否健康
// 注意：本方法不走统一响应封装，K8s 探针需要固定格式
func (ctl *Controller) Ready(c *gin.Context) {
	checks := gin.H{}
	allHealthy := true

	// MySQL 检查
	if ctl.conf.MySQL.Host != "" {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()

		sqlDB, err := ctl.provider.UseDB(ctx).DB()
		if err != nil || sqlDB.PingContext(ctx) != nil {
			checks["mysql"] = "unhealthy"
			allHealthy = false
		} else {
			checks["mysql"] = "ok"
		}
	}

	// Redis 检查（未启用时跳过）
	if len(ctl.conf.Redis.Addr) == 0 || ctl.conf.Redis.Addr[0] == "" {
		checks["redis"] = redisDisabled
	} else if ctl.redis == nil {
		checks["redis"] = "not_initialized"
		allHealthy = false
	} else {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()

		if err := ctl.redis.Ping(ctx).Err(); err != nil {
			checks["redis"] = "unhealthy: " + err.Error()
			allHealthy = false
		} else {
			checks["redis"] = "ok"
		}
	}

	if allHealthy {
		c.JSON(http.StatusOK, gin.H{"status": "ready", "checks": checks})
		return
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"status": "not_ready", "checks": checks})
}
```

- [ ] **Step 2: 写 `infrastructure/controller/http/knowledge/controller.go`**

```go
package knowledge

import (
	"fmt"

	appsvc "github.com/PycMono/FastRAG/application/service/knowledge"
	"github.com/PycMono/FastRAG/common/dto"
	apperrors "github.com/PycMono/FastRAG/common/errors"
	"github.com/PycMono/FastRAG/infrastructure/driver/gingext"
	"github.com/PycMono/go-context-sdk/bizctx"
	"github.com/gin-gonic/gin"
)

// Controller 知识库 HTTP 控制器
type Controller struct {
	service *appsvc.Service
}

// NewController 创建控制器（fx 注入）
func NewController(service *appsvc.Service) *Controller {
	return &Controller{service: service}
}

// getUserID 从 bizctx 取用户标识
// 由 go-gin-sdk 的 middleware.Bizctx() 从请求头 X-Bizctx-UserID 注入
func getUserID(c *gin.Context) string {
	return bizctx.GetUserID(c.Request.Context())
}

// Create POST /api/v1/knowledge-bases
func (ctl *Controller) Create(c *gin.Context) {
	userID := getUserID(c)
	if userID == "" {
		gingext.Send(c, nil, apperrors.ErrUnauthorized)
		return
	}

	var param dto.CreateKnowledgeBaseDTO
	if err := c.ShouldBindJSON(&param); err != nil {
		gingext.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	result, err := ctl.service.Create(c.Request.Context(), userID, param)
	gingext.Send(c, result, err)
}

// List GET /api/v1/knowledge-bases
func (ctl *Controller) List(c *gin.Context) {
	userID := getUserID(c)
	if userID == "" {
		gingext.Send(c, nil, apperrors.ErrUnauthorized)
		return
	}

	var query dto.ListKnowledgeBaseQuery
	if err := c.ShouldBindQuery(&query); err != nil {
		gingext.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	result, err := ctl.service.List(c.Request.Context(), userID, query)
	gingext.Send(c, result, err)
}

// Get GET /api/v1/knowledge-bases/:id
func (ctl *Controller) Get(c *gin.Context) {
	userID := getUserID(c)
	if userID == "" {
		gingext.Send(c, nil, apperrors.ErrUnauthorized)
		return
	}

	result, err := ctl.service.Get(c.Request.Context(), userID, c.Param("id"))
	gingext.Send(c, result, err)
}

// Update PUT /api/v1/knowledge-bases/:id
func (ctl *Controller) Update(c *gin.Context) {
	userID := getUserID(c)
	if userID == "" {
		gingext.Send(c, nil, apperrors.ErrUnauthorized)
		return
	}

	var param dto.UpdateKnowledgeBaseDTO
	if err := c.ShouldBindJSON(&param); err != nil {
		gingext.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	err := ctl.service.Update(c.Request.Context(), userID, c.Param("id"), param)
	gingext.Send(c, nil, err)
}

// Delete DELETE /api/v1/knowledge-bases/:id
func (ctl *Controller) Delete(c *gin.Context) {
	userID := getUserID(c)
	if userID == "" {
		gingext.Send(c, nil, apperrors.ErrUnauthorized)
		return
	}

	err := ctl.service.Delete(c.Request.Context(), userID, c.Param("id"))
	gingext.Send(c, nil, err)
}

// Search POST /api/v1/knowledge-bases/:id/search
func (ctl *Controller) Search(c *gin.Context) {
	userID := getUserID(c)
	if userID == "" {
		gingext.Send(c, nil, apperrors.ErrUnauthorized)
		return
	}

	var param dto.SearchKnowledgeBaseDTO
	if err := c.ShouldBindJSON(&param); err != nil {
		gingext.Send(c, nil, fmt.Errorf("%w: %v", apperrors.ErrInvalidParam, err))
		return
	}

	result, err := ctl.service.Search(c.Request.Context(), userID, c.Param("id"), param)
	gingext.Send(c, result, err)
}
```

- [ ] **Step 3: 写 `infrastructure/controller/http/register.go`**

```go
package http

import (
	"github.com/PycMono/FastRAG/infrastructure/controller/http/health"
	"github.com/PycMono/FastRAG/infrastructure/controller/http/knowledge"
	"github.com/gin-gonic/gin"
	"go.uber.org/fx"
)

const v1Prefix = "/api/v1"

// Register 注册所有 HTTP 控制器到 FX 容器
var Register = fx.Options(
	fx.Provide(health.NewController),
	fx.Provide(knowledge.NewController),
	fx.Invoke(RegisterRoutes),
)

// RouteDeps 路由注册所需依赖（FX 自动按类型注入）
type RouteDeps struct {
	fx.In
	Router       *gin.Engine
	HealthCtl    *health.Controller
	KnowledgeCtl *knowledge.Controller
}

// RegisterRoutes 统一入口
// 页面/探针路由注册在顶层 router，所有 JSON API 路由注册在 api Group 下
func RegisterRoutes(d RouteDeps) {
	// 健康检查（顶层，非 /api 前缀）
	registerHealthRoutes(d.Router, d.HealthCtl)

	// API 路由（必须挂在 api Group 下）
	api := d.Router.Group(v1Prefix)
	{
		registerKnowledgeRoutes(api, d.KnowledgeCtl)
	}
}

func registerHealthRoutes(r *gin.Engine, healthCtl *health.Controller) {
	r.GET("/health", healthCtl.Health)
	r.GET("/ready", healthCtl.Ready)
}

func registerKnowledgeRoutes(api *gin.RouterGroup, ctl *knowledge.Controller) {
	g := api.Group("/knowledge-bases")
	{
		g.POST("", ctl.Create)
		g.GET("", ctl.List)
		g.GET("/:id", ctl.Get)
		g.PUT("/:id", ctl.Update)
		g.DELETE("/:id", ctl.Delete)

		// 检索扩展点（当前返回 ErrKnowledgeRetrievalNotImpl）
		g.POST("/:id/search", ctl.Search)
	}
}
```

- [ ] **Step 4: 写 `infrastructure/controller/register.go`**

```go
package controller

import (
	"github.com/PycMono/FastRAG/infrastructure/controller/http"
	"go.uber.org/fx"
)

// Register 注册所有控制器
var Register = fx.Options(
	http.Register,
)
```

- [ ] **Step 5: 验证编译**

```bash
export PATH="/Users/allen/projects/gosdk/go1.25.8/bin:$PATH"
export GOPATH=/tmp/gopath GOMODCACHE=/tmp/gopath/pkg/mod GOFLAGS=-mod=mod
export GOPRIVATE="github.com/PycMono/*"
cd /Users/allen/projects/work/github/FastRAG
gofmt -l infrastructure/controller/ && go vet ./infrastructure/controller/... && go build ./...
```

Expected: 无输出，退出码 0。

- [ ] **Step 6: 提交**

```bash
git add infrastructure/controller/
git commit -m "feat: 新增健康检查与知识库 HTTP 控制器及路由注册"
```

---

### Task 11: FX 装配与启动入口

**Files:**
- Create: `infrastructure/init.go`
- Create: `cmd/server/main.go`

**Interfaces:**
- Consumes: 前面所有任务的产物
- Produces: `infrastructure.Init(conf *config.Config) fx.Option`、可执行入口 `cmd/server`

- [ ] **Step 1: 写 `infrastructure/init.go`**

```go
package infrastructure

import (
	"github.com/PycMono/FastRAG/application/service"
	"github.com/PycMono/FastRAG/domain"
	"github.com/PycMono/FastRAG/infrastructure/config"
	"github.com/PycMono/FastRAG/infrastructure/controller"
	"github.com/PycMono/FastRAG/infrastructure/driver/gingext"
	"github.com/PycMono/FastRAG/infrastructure/driver/mysql"
	"github.com/PycMono/FastRAG/infrastructure/driver/redis"
	"github.com/PycMono/FastRAG/infrastructure/persistence"
	"github.com/PycMono/FastRAG/infrastructure/serviceimpl"
	"go.uber.org/fx"
)

// Init 初始化所有基础设施组件
func Init(conf *config.Config) fx.Option {
	return fx.Options(
		// 提供配置
		fx.Supply(conf),

		// 提供 Redis 客户端（go-cache-sdk，addr 为空则不启用）
		fx.Provide(redis.NewClient),

		// 提供 MySQL Provider（go-mysql-sdk）
		fx.Provide(mysql.NewProvider),

		// 提供 Gin 引擎和 HTTP Server（go-gin-sdk）
		fx.Provide(gingext.NewEngine),
		fx.Provide(gingext.NewHTTPServer),

		// 注册控制器（HTTP 入口）
		controller.Register,

		// 注册应用服务
		service.Register,

		// 注册领域层
		domain.Register,

		// 注册持久化实现
		persistence.Register,

		// 注册基础设施服务（ID 生成器等）
		serviceimpl.Register,
	)
}
```

- [ ] **Step 2: 写 `cmd/server/main.go`**

```go
package main

import (
	"context"

	"github.com/PycMono/FastRAG/domain/entity/knowledge"
	"github.com/PycMono/FastRAG/infrastructure"
	"github.com/PycMono/FastRAG/infrastructure/config"
	ginsdk "github.com/PycMono/go-gin-sdk"
	logsdk "github.com/PycMono/go-logger-sdk"
	sqlsdk "github.com/PycMono/go-mysql-sdk"
	"go.uber.org/fx"
)

func main() {
	logsdk.SetLogger(logsdk.NewLogrus(logsdk.Options{Module: "fastrag"}))

	conf := config.MustLoad()

	app := fx.New(
		infrastructure.Init(conf),
		fx.Invoke(func(lifecycle fx.Lifecycle, server *ginsdk.HTTPServer, provider sqlsdk.Provider) {
			lifecycle.Append(fx.Hook{
				OnStart: func(ctx context.Context) error {
					// AutoMigrate 数据库表
					if err := provider.UseDB(ctx).AutoMigrate(
						&knowledge.KnowledgeBase{},
					); err != nil {
						logsdk.Error(ctx, "auto migrate failed", logsdk.Err(err))
						return err
					}
					logsdk.Info(ctx, "auto migrate completed")

					logsdk.Info(ctx, "HTTP server starting", logsdk.Any("port", conf.HTTP.Port))
					go func() {
						server.Serve(ctx)
					}()
					return nil
				},
				OnStop: func(ctx context.Context) error {
					logsdk.Info(ctx, "HTTP server shutting down")
					return server.Shutdown(ctx)
				},
			})
		}),
	)

	app.Run()
}
```

- [ ] **Step 3: 验证编译与依赖图（不需要数据库也能验证 DI 装配）**

`fx.New` 会在启动时解析整个依赖图，因此即使 MySQL 未运行，只要报错信息指向 MySQL
连接失败（而不是 `missing type` / `cycle detected`），就说明 DI 装配正确。

```bash
export PATH="/Users/allen/projects/gosdk/go1.25.8/bin:$PATH"
export GOPATH=/tmp/gopath GOMODCACHE=/tmp/gopath/pkg/mod GOFLAGS=-mod=mod
export GOPRIVATE="github.com/PycMono/*"
cd /Users/allen/projects/work/github/FastRAG
gofmt -l . && go vet ./... && go build -o /tmp/fastrag-server ./cmd/server && echo "BUILD_OK"
```

Expected: `gofmt -l` 无输出；`go vet` 退出码 0；打印 `BUILD_OK`。

- [ ] **Step 4: 提交**

```bash
git add infrastructure/init.go cmd/
git commit -m "feat: 新增 FX 全量装配与 cmd/server 启动入口"
```

---

### Task 12: README、架构 linter 与端到端验证

**Files:**
- Create: `README.md`
- Create: `scripts/lint-architecture.sh`

**Interfaces:**
- Consumes: 全部
- Produces: 文档与分层红线检查脚本

- [ ] **Step 1: 写 `scripts/lint-architecture.sh`**

```bash
#!/bin/bash
# 架构 linter — 在开发阶段拦截架构违规
# 用法: bash scripts/lint-architecture.sh
# 返回 0 表示通过，返回 1 表示有 Blocker 级违规

set -e

ERRORS=0

echo "=== Architecture Linter ==="

# 1. domain/ 禁止 import gin/gorm/redis/http
echo "→ Checking domain/ import redlines..."
if grep -rn "gin-gonic/gin\|gorm.io/gorm\|go-redis/v9\|net/http" domain/ 2>/dev/null | grep -v "_test.go" | grep -v "// "; then
    echo "BLOCKER: domain/ imports forbidden package (gin/gorm/redis/http)"
    ERRORS=$((ERRORS + 1))
else
    echo "  ✅ domain/ imports clean"
fi

# 2. application/ 禁止 import gin/redis driver/mysql driver/controller
echo "→ Checking application/ import redlines..."
if grep -rn "gin-gonic/gin\|driver/redis\|driver/mysql\|infrastructure/controller" application/ 2>/dev/null | grep -v "_test.go" | grep -v "// "; then
    echo "BLOCKER: application/ imports forbidden package (gin/redis driver/mysql driver/controller)"
    ERRORS=$((ERRORS + 1))
else
    echo "  ✅ application/ imports clean"
fi

# 3. common/ 禁止 import domain/application/infrastructure
echo "→ Checking common/ import redlines..."
if grep -rnE '"github.com/PycMono/FastRAG/domain/|"github.com/PycMono/FastRAG/application/|"github.com/PycMono/FastRAG/infrastructure/' common/ 2>/dev/null | grep -v "_test.go"; then
    echo "BLOCKER: common/ imports business layer (domain/application/infrastructure)"
    ERRORS=$((ERRORS + 1))
else
    echo "  ✅ common/ imports clean"
fi

# 4. Controller 禁止直接返回 Entity（启发式：Controller 不应 import domain/entity）
echo "→ Checking Controller entity leakage..."
for f in infrastructure/controller/http/*/*.go; do
    if [ -f "$f" ]; then
        if grep -q '"github.com/PycMono/FastRAG/domain/entity' "$f"; then
            echo "MAJOR: Controller $f imports domain/entity — may return Entity instead of VO"
            ERRORS=$((ERRORS + 1))
        fi
    fi
done
echo "  ✅ Controller entity check done"

# 5. API 路由必须在 api Group 下
echo "→ Checking API route grouping..."
if grep -rnE '\.(GET|POST|PUT|DELETE)\("/?api' infrastructure/controller/ 2>/dev/null; then
    echo "BLOCKER: API route registered on top-level router instead of api Group"
    ERRORS=$((ERRORS + 1))
else
    echo "  ✅ API route grouping check done"
fi

# 6. 检查是否使用 gingext.Send 而非 c.JSON（health 探针豁免）
echo "→ Checking unified response format..."
for f in infrastructure/controller/http/*/*.go; do
    if [ -f "$f" ]; then
        if [ "$(dirname "$f")" = "infrastructure/controller/http/health" ]; then
            echo "  ⏭️  Skipping $f (health probe needs fixed format)"
            continue
        fi
        if grep -n 'c\.JSON(' "$f" 2>/dev/null | grep -v '//'; then
            echo "BLOCKER: $f uses c.JSON() instead of gingext.Send()"
            ERRORS=$((ERRORS + 1))
        fi
    fi
done
echo "  ✅ Response format check done"

# 总结
echo ""
if [ $ERRORS -gt 0 ]; then
    echo "=== FAILED: $ERRORS architecture violation(s) found ==="
    exit 1
else
    echo "=== PASSED: All architecture checks passed ==="
    exit 0
fi
```

- [ ] **Step 2: 写 `README.md`**

````markdown
# FastRAG

知识库检索服务 — Go 洋葱架构后端骨架。

参考 `micro-framework` 模板构建，仅包含后端代码（无前端）。

## 架构介绍

本项目采用洋葱架构（Onion Architecture），核心思想是分离业务复杂性与技术复杂性，使领域层独立于基础设施。

分层结构（外层依赖内层，内层不感知外层）：

```
cmd/server              // 服务启动入口
application/service     // 应用服务层：编排领域服务，处理用例，DTO→Entity→VO 转换
domain
├── entity              // 领域模型（聚合根、实体、值对象）
├── repository          // 仓储接口（持久化抽象）
└── service             // 领域服务（核心业务逻辑）
infrastructure
├── config              // 配置加载
├── controller/http     // HTTP 请求入口、路由绑定（北向网关）
├── driver              // 第三方 SDK 适配（gingext / mysql / redis）
├── middleware          // 限流、访问日志
├── persistence         // 仓储实现、数据持久化（南向网关）
└── serviceimpl         // 基础设施服务实现（雪花 ID）
common
├── dto                 // 入参定义
├── vo                  // 返回值定义
└── errors              // 业务错误码与预定义错误
```

依赖注入使用 [Uber FX](https://github.com/uber-go/fx)。

### 依赖方向红线

| 层 | 禁止 import |
|----|-------------|
| `domain/` | `gin`、`gorm`、`redis`、`net/http`、`infrastructure/...`、`application/...` |
| `application/` | `gin`、`infrastructure/driver/...`、`infrastructure/controller` |
| `common/` | `domain/...`、`application/...`、`infrastructure/...` |

用 `make lint` 检查。

## 快速开始

前置：Go 1.25+、MySQL。Redis 可选（`config.json` 的 `redis.addr` 留空即不启用）。

```bash
# 下载依赖
go mod download

# 调整 config.json 中的 MySQL 连接信息，然后运行
go run ./cmd/server
# 或
make run
```

服务默认监听 `:8080`，首次启动会自动建表（`knowledge_bases`）。

## 常用命令

```bash
make build        # 构建二进制到 bin/server
make run          # 直接运行服务
make test         # 运行所有测试（含 race 检测和覆盖率）
make test-pkg     # 运行指定包测试，如 make test-pkg PKG=application/service/knowledge
make test-single  # 运行单个测试，如 make test-single NAME=TestService_Create PKG=application/service/knowledge
make lint         # 架构红线检查
make tidy         # 整理 go.mod
make clean        # 清理构建产物
```

## 统一响应格式

所有 API 均通过 `gingext.Send` 返回：

```jsonc
// 成功
{"code": 0, "msg": "success", "data": {...}}
// 参数错误（HTTP 400）
{"code": 10001, "msg": "invalid parameter", "data": {}}
// 未找到（HTTP 404）
{"code": 10101, "msg": "knowledge base not found", "data": {}}
// 其他业务/系统错误（HTTP 200 + 业务码）
{"code": 10103, "msg": "knowledge base create failed", "data": {}}
```

### 错误码

| 码段 | 含义 |
|------|------|
| `10001`–`10007` | 通用：参数错误 / 未登录 / 无权限 / 不存在 / 冲突 / 限流 / 内部错误 |
| `10101`–`10106` | knowledge：不存在 / 名称重复 / 创建失败 / 更新失败 / 删除失败 / 检索未实现 |

错误定义集中在 `common/errors/errors.go`。新增错误码时在此追加，不要在业务代码里随手 `errors.New`。

错误分为两类：

- `BizError`（预期内，如「知识库不存在」）
- `SysError`（非预期，如「数据库连接失败」）

## 用户身份

骨架阶段尚未接入鉴权模块。`middleware.Bizctx()`（来自 go-gin-sdk）会把请求头
`X-Bizctx-UserID` 解析进 `bizctx`，Controller 通过 `bizctx.GetUserID()` 读取。

```bash
curl -H "X-Bizctx-UserID: u1" http://localhost:8080/api/v1/knowledge-bases
```

接入真实鉴权后，在中间件链里把 session/JWT 解析出的 userID 写入 `bizctx` 即可，
Controller 与 Application Service 无需改动。

## API 示例

```bash
# 创建知识库
curl -X POST http://localhost:8080/api/v1/knowledge-bases \
  -H "Content-Type: application/json" \
  -H "X-Bizctx-UserID: u1" \
  -d '{"name":"产品手册","description":"内部产品文档"}'

# 列表（分页）
curl -H "X-Bizctx-UserID: u1" "http://localhost:8080/api/v1/knowledge-bases?page=1&page_size=10"

# 详情
curl -H "X-Bizctx-UserID: u1" http://localhost:8080/api/v1/knowledge-bases/{id}

# 更新
curl -X PUT http://localhost:8080/api/v1/knowledge-bases/{id} \
  -H "Content-Type: application/json" \
  -H "X-Bizctx-UserID: u1" \
  -d '{"name":"产品手册 v2"}'

# 删除
curl -X DELETE -H "X-Bizctx-UserID: u1" http://localhost:8080/api/v1/knowledge-bases/{id}

# 检索（扩展点，当前返回 code 10106 未实现）
curl -X POST http://localhost:8080/api/v1/knowledge-bases/{id}/search \
  -H "Content-Type: application/json" \
  -H "X-Bizctx-UserID: u1" \
  -d '{"query":"如何配置","top_k":5}'

# 健康检查
curl http://localhost:8080/health
curl http://localhost:8080/ready
```

## 如何新增一个业务模块

以 `order` 为例，照抄 `knowledge` 模块的分层写法：

1. **domain 层**：`domain/entity/order/order.go`、`domain/repository/order/order.go`（仓储接口）
2. **common 层**：`common/dto/order.go`、`common/vo/order.go`，错误码追加到 `common/errors/errors.go`
3. **application 层**：`application/service/order/service.go`，并在 `application/service/register.go` 里 `fx.Provide`
4. **infrastructure 层**：`infrastructure/persistence/order/order_repo.go`（实现仓储接口）、
   `infrastructure/controller/http/order/controller.go`，并在对应的 `register.go` 里注册
5. **建表**：把新实体加进 `cmd/server/main.go` 的 `AutoMigrate` 列表
6. **路由**：在 `infrastructure/controller/http/register.go` 的 `api` Group 下挂载

## 后续规划

- 接入向量库：实现 `domain/repository/knowledge.IKnowledgeRetriever`，
  在 `infrastructure/persistence/register.go` 中替换 `RetrieverStub`，其余各层无需改动
- 文档入库与切片：新增 `document` 模块
- 鉴权：见上文「用户身份」
````

- [ ] **Step 3: 运行架构 linter 与全量测试**

```bash
export PATH="/Users/allen/projects/gosdk/go1.25.8/bin:$PATH"
export GOPATH=/tmp/gopath GOMODCACHE=/tmp/gopath/pkg/mod GOFLAGS=-mod=mod
export GOPRIVATE="github.com/PycMono/*"
cd /Users/allen/projects/work/github/FastRAG
bash scripts/lint-architecture.sh
go test -race ./...
```

Expected: linter 输出 `=== PASSED: All architecture checks passed ===`；所有测试 PASS。

- [ ] **Step 4: 整理依赖并最终校验**

```bash
export PATH="/Users/allen/projects/gosdk/go1.25.8/bin:$PATH"
export GOPATH=/tmp/gopath GOMODCACHE=/tmp/gopath/pkg/mod GOFLAGS=-mod=mod
export GOPRIVATE="github.com/PycMono/*"
cd /Users/allen/projects/work/github/FastRAG
go mod tidy
gofmt -l .
go build ./...
go vet ./...
go test ./...
go mod tidy && git diff --exit-code go.mod go.sum && echo "TIDY_STABLE"
```

Expected: `gofmt -l` 无输出；全部退出码 0；打印 `TIDY_STABLE`（说明 tidy 后无变化）。

- [ ] **Step 5: 端到端验证（需要 Docker；若 Docker 不可用则跳过并记录）**

```bash
# 起一个临时 MySQL
docker run --rm -d --name fastrag-e2e-mysql \
  -p 13306:3306 \
  -e MYSQL_ROOT_PASSWORD=123456 \
  -e MYSQL_DATABASE=fastrag \
  mysql:8

# 等待 MySQL 就绪
for i in $(seq 1 60); do
  docker exec fastrag-e2e-mysql mysqladmin ping -h127.0.0.1 -uroot -p123456 --silent 2>/dev/null && break
  sleep 2
done
```

然后用一份临时配置（MySQL 指向 `127.0.0.1:13306`）启动服务：

```bash
export PATH="/Users/allen/projects/gosdk/go1.25.8/bin:$PATH"
export GOPATH=/tmp/gopath GOMODCACHE=/tmp/gopath/pkg/mod GOFLAGS=-mod=mod
export GOPRIVATE="github.com/PycMono/*"
cd /Users/allen/projects/work/github/FastRAG

# 备份原配置，换成 E2E 配置
cp config.json /tmp/config.json.bak
sed 's/"port": 3306/"port": 13306/' config.json > /tmp/config.e2e.json && cp /tmp/config.e2e.json config.json

go build -o /tmp/fastrag-server ./cmd/server
/tmp/fastrag-server > /tmp/fastrag-server.log 2>&1 &
SERVER_PID=$!
sleep 3
```

验证脚本（依次执行，逐条检查输出）：

```bash
BASE=http://localhost:8080
H='-H Content-Type:application/json -H X-Bizctx-UserID:u1'

echo "--- health ---"
curl -s $BASE/health

echo "--- ready ---"
curl -s $BASE/ready

echo "--- create ---"
curl -s -X POST $BASE/api/v1/knowledge-bases $H -d '{"name":"产品手册","description":"内部文档"}'

echo "--- missing user id should be 10002 ---"
curl -s -X POST $BASE/api/v1/knowledge-bases -H 'Content-Type: application/json' -d '{"name":"x"}'

echo "--- invalid param should be 10001 ---"
curl -s -X POST $BASE/api/v1/knowledge-bases $H -d '{}'

echo "--- list ---"
curl -s -H 'X-Bizctx-UserID: u1' "$BASE/api/v1/knowledge-bases?page=1&page_size=10"
```

Expected:
- `/health` 返回 `{"status":"up",...}`；`/ready` 返回 `status: ready`，`checks.redis` 为 `disabled`
- 创建返回 `code: 0` 且 `data.id` 非空 —— **记录这个 id 为 KB_ID**
- 缺 `X-Bizctx-UserID` 返回 `code: 10002`
- 空 body 返回 `code: 10001`（验证 `errors.As` 修复生效）
- 列表返回 `code: 0` 且 `total: 1`

继续验证详情 / 更新 / 检索扩展点 / 跨用户隔离：

```bash
KB_ID=<上一步返回的 data.id>

echo "--- get ---"
curl -s -H 'X-Bizctx-UserID: u1' $BASE/api/v1/knowledge-bases/$KB_ID

echo "--- update ---"
curl -s -X PUT $BASE/api/v1/knowledge-bases/$KB_ID $H -d '{"name":"产品手册 v2"}'
curl -s -H 'X-Bizctx-UserID: u1' $BASE/api/v1/knowledge-bases/$KB_ID

echo "--- cross-user isolation should be 10101 + HTTP 404 ---"
curl -s -o /dev/null -w 'http_status=%{http_code}\n' -H 'X-Bizctx-UserID: u2' $BASE/api/v1/knowledge-bases/$KB_ID
curl -s -H 'X-Bizctx-UserID: u2' $BASE/api/v1/knowledge-bases/$KB_ID

echo "--- search extension point should be 10106 ---"
curl -s -X POST $BASE/api/v1/knowledge-bases/$KB_ID/search $H -d '{"query":"如何配置","top_k":5}'

echo "--- delete ---"
curl -s -X DELETE $BASE/api/v1/knowledge-bases/$KB_ID -H 'X-Bizctx-UserID: u1'
curl -s -H 'X-Bizctx-UserID: u1' $BASE/api/v1/knowledge-bases/$KB_ID
```

Expected: 更新后 `name` 为 `产品手册 v2`；跨用户访问返回 `code: 10101` 且 `http_status=404`；
检索返回 `code: 10106`；删除后详情返回 `code: 10101`。

收尾（务必执行）：

```bash
kill $SERVER_PID
cp /tmp/config.json.bak /Users/allen/projects/work/github/FastRAG/config.json
docker stop fastrag-e2e-mysql
cat /tmp/fastrag-server.log | tail -20
```

Expected: 恢复原 `config.json`（`git status` 应显示 `config.json` 无改动），容器停止。

- [ ] **Step 6: 提交**

```bash
git add README.md scripts/
git commit -m "docs: 新增 README 与架构红线 linter"
```

---

## 完成标准

- [ ] `go build ./...`、`go vet ./...`、`go test -race ./...` 全部通过
- [ ] `gofmt -l .` 无输出
- [ ] `bash scripts/lint-architecture.sh` 输出 PASSED
- [ ] `go mod tidy` 后 `go.mod`/`go.sum` 无变化
- [ ] 端到端验证全部符合预期（或 Docker 不可用时明确记录为未验证）
- [ ] 无任何前端相关文件（`frontend/`、`page/` 控制器、模板、静态资源）
