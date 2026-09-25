package logger

import "errors"

type Option func(*Log) error

func WithLevel(level string) Option {
	return func(l *Log) error {
		v, err := extractLevel(level)
		if err != nil {
			return err
		}
		l.level = v
		return nil
	}
}
func WithStdOut(enabled bool) Option { return func(l *Log) error { l.stdout = enabled; return nil } }
func WithFile(path string) Option {
	return func(l *Log) error {
		if path == "" {
			return errors.New("log file path is empty")
		}
		l.paths = append(l.paths, path)
		return nil
	}
}
