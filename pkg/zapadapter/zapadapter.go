package zapadapter

import (
	"fmt"

	temporallog "go.temporal.io/sdk/log"
	"go.uber.org/zap"
)

type ZapAdapter struct {
	zl *zap.Logger
}

func NewZapAdapter(zapLogger *zap.Logger) *ZapAdapter {
	return &ZapAdapter{
		zl: zapLogger.WithOptions(zap.AddCallerSkip(1)),
	}
}

func (adapter *ZapAdapter) fields(keyvals []any) []zap.Field {
	if len(keyvals)%2 != 0 {
		return []zap.Field{zap.Error(fmt.Errorf("odd number of keyvals pairs: %v", keyvals))}
	}

	var fields []zap.Field

	for i := 0; i < len(keyvals); i += 2 {
		key, ok := keyvals[i].(string)
		if !ok {
			key = fmt.Sprintf("%v", keyvals[i])
		}

		fields = append(fields, zap.Any(key, keyvals[i+1]))
	}

	return fields
}

func (adapter *ZapAdapter) Debug(msg string, keyvals ...any) {
	adapter.zl.Debug(msg, adapter.fields(keyvals)...)
}

func (adapter *ZapAdapter) Info(msg string, keyvals ...any) {
	adapter.zl.Info(msg, adapter.fields(keyvals)...)
}

func (adapter *ZapAdapter) Warn(msg string, keyvals ...any) {
	adapter.zl.Warn(msg, adapter.fields(keyvals)...)
}

func (adapter *ZapAdapter) Error(msg string, keyvals ...any) {
	adapter.zl.Error(msg, adapter.fields(keyvals)...)
}

func (adapter *ZapAdapter) With(keyvals ...any) temporallog.Logger {
	return &ZapAdapter{zl: adapter.zl.With(adapter.fields(keyvals)...)}
}

func (adapter *ZapAdapter) WithCallerSkip(skip int) temporallog.Logger {
	return &ZapAdapter{zl: adapter.zl.WithOptions(zap.AddCallerSkip(skip))}
}
