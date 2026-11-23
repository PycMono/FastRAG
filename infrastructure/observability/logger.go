package observability

import (
	"context"

	logsdk "github.com/PycMono/go-logger-sdk"
)

type Logger struct {
	logsdk.Logger
	Store *Store
}

func (l Logger) record(ctx context.Context, level, message string, fields []logsdk.Fields) {
	merged := map[string]any{}
	for _, f := range fields {
		for k, v := range f {
			merged[k] = v
		}
	}
	for k, v := range logsdk.TraceFields(ctx, nil) {
		merged[k] = v
	}
	l.Store.Add(level, message, merged)
}
func (l Logger) Info(ctx context.Context, msg string, f ...logsdk.Fields) {
	l.record(ctx, "info", msg, f)
	l.Logger.Info(ctx, msg, f...)
}
func (l Logger) Error(ctx context.Context, msg string, f ...logsdk.Fields) {
	l.record(ctx, "error", msg, f)
	l.Logger.Error(ctx, msg, f...)
}
func (l Logger) Warn(ctx context.Context, msg string, f ...logsdk.Fields) {
	l.record(ctx, "warn", msg, f)
	l.Logger.Warn(ctx, msg, f...)
}
func (l Logger) Debug(ctx context.Context, msg string, f ...logsdk.Fields) {
	l.record(ctx, "debug", msg, f)
	l.Logger.Debug(ctx, msg, f...)
}
func (l Logger) Fatal(ctx context.Context, msg string, f ...logsdk.Fields) {
	l.record(ctx, "error", msg, f)
	l.Logger.Fatal(ctx, msg, f...)
}
func (l Logger) Panic(ctx context.Context, msg string, f ...logsdk.Fields) {
	l.record(ctx, "error", msg, f)
	l.Logger.Panic(ctx, msg, f...)
}
