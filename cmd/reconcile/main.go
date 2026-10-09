// 计数对账：重算某个知识库的 doc_count / chunk_count 并回写。
//
// 计数是用差值维护的（§5.1），删除和异常会让它慢慢漂移。这个命令是兜底：
// 定时（或人工）跑一次，把数字拉回与真实数据一致。
//
// 用法:
//
//	go run ./cmd/reconcile -account acc_001 -kb KB20261008001
package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/PycMono/FastRAG/application/service/ingest"
	"github.com/PycMono/FastRAG/infrastructure"
	"github.com/PycMono/FastRAG/infrastructure/config"
	logsdk "github.com/PycMono/go-logger-sdk"
	"go.uber.org/fx"
)

func main() {
	account := flag.String("account", "", "租户（必填）")
	kbNo := flag.String("kb", "", "知识库编号（必填）")
	flag.Parse()

	if *account == "" || *kbNo == "" {
		flag.Usage()
		return
	}

	logsdk.SetLogger(logsdk.NewLogrus(logsdk.Options{Module: "fastrag-reconcile"}))

	app := fx.New(
		infrastructure.InitCLI(config.MustLoad()),
		fx.Invoke(func(svc *ingest.Service) error {
			ctx := context.Background()

			res, err := svc.Reconcile(ctx, *kbNo, *account)
			if err != nil {
				logsdk.Error(ctx, "对账失败", logsdk.Err(err))
				return err
			}

			logsdk.Info(ctx, "对账完成",
				logsdk.Any("kb_no", res.KBNo),
				logsdk.Any("doc_count", fmt.Sprintf("%d -> %d", res.DocCountBefore, res.DocCountAfter)),
				logsdk.Any("chunk_count", fmt.Sprintf("%d -> %d", res.ChunkCountBefore, res.ChunkCountAfter)),
			)
			return nil
		}),
	)
	app.Run()
}
