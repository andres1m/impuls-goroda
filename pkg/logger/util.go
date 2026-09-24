package logger

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"go.uber.org/zap/zapcore"
)

func extractLevel(level string) (zapcore.Level, error) {
	supportedLogLevels := map[string]zapcore.Level{
		"debug": zapcore.DebugLevel,
		"info":  zapcore.InfoLevel,
		"warn":  zapcore.WarnLevel,
		"error": zapcore.ErrorLevel,
	}

	lvl, found := supportedLogLevels[level]
	if !found {
		return 0, errors.New("logging level not found")
	}

	return lvl, nil
}

func checkOrCreatePath(p string) (string, error) {
	d := filepath.Dir(p)

	_, err := os.Stat(d)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("path %s does not exist: %w", d, err)
	}

	if err := os.MkdirAll(d, 0750); err != nil {
		return "", fmt.Errorf("failed to create directory %s: %w", d, err)
	}

	return p, nil
}
