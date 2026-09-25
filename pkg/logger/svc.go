package logger

import (
	"context"
	"errors"
	"syscall"

	"github.com/andres1m/impuls-goroda/pkg/svc"
)

func (l *Log) Name() string                      { return "logger" }
func (l *Log) DependsOn() []string               { return nil }
func (l *Log) Init(context.Context) error        { return nil }
func (l *Log) HealthCheck(context.Context) error { return nil }
func (l *Log) Run(context.Context) error         { return nil }
func (l *Log) Stop(context.Context) error {
	l.stopOnce.Do(func() {
		if l.Log != nil {
			err := l.Log.Sync()
			if !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTTY) {
				l.stopErr = err
			}
		}
		l.stopErr = errors.Join(l.stopErr, l.closeFiles())
	})
	return l.stopErr
}

var _ svc.Service = (*Log)(nil)
