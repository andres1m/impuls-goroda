package logger

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Log struct {
	Log      *zap.Logger
	level    zapcore.Level
	stdout   bool
	paths    []string
	files    []*os.File
	stopOnce sync.Once
	stopErr  error
}

func New(opts ...Option) (*Log, error) {
	l := &Log{level: zapcore.InfoLevel}
	for _, opt := range opts {
		if err := opt(l); err != nil {
			return nil, err
		}
	}
	if !l.stdout && len(l.paths) == 0 {
		return nil, errors.New("no log outputs configured")
	}
	ec := zap.NewProductionEncoderConfig()
	ec.EncodeTime = zapcore.ISO8601TimeEncoder
	ec.CallerKey = "caller"
	encoder := zapcore.NewJSONEncoder(ec)
	var cores []zapcore.Core
	if l.stdout {
		cores = append(cores,
			zapcore.NewCore(encoder, zapcore.Lock(zapcore.AddSync(os.Stdout)), zap.LevelEnablerFunc(func(v zapcore.Level) bool { return v >= l.level && v < zapcore.WarnLevel })),
			zapcore.NewCore(encoder, zapcore.Lock(zapcore.AddSync(os.Stderr)), zap.LevelEnablerFunc(func(v zapcore.Level) bool { return v >= l.level && v >= zapcore.WarnLevel })))
	}
	for _, path := range l.paths {
		path, err := checkOrCreatePath(path)
		if err != nil {
			l.closeFiles()
			return nil, err
		}
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			l.closeFiles()
			return nil, fmt.Errorf("open log file: %w", err)
		}
		l.files = append(l.files, f)
		cores = append(cores, zapcore.NewCore(encoder, zapcore.Lock(zapcore.AddSync(f)), l.level))
	}
	l.Log = zap.New(zapcore.NewTee(cores...), zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel))
	return l, nil
}
func (l *Log) closeFiles() error {
	var err error
	for _, f := range l.files {
		err = errors.Join(err, f.Close())
	}
	return err
}
