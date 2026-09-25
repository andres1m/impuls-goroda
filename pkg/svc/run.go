package svc

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

type Options struct{ ShutdownTimeout time.Duration }

func Run(ctx context.Context, log *zap.Logger, services []Service) error {
	return RunWithOptions(ctx, log, services, Options{ShutdownTimeout: 10 * time.Second})
}

func RunWithOptions(ctx context.Context, log *zap.Logger, services []Service, opts Options) (result error) {
	if opts.ShutdownTimeout <= 0 {
		return errors.New("shutdown timeout must be positive")
	}
	sorted, err := sortTopologically(services)
	if err != nil {
		return err
	}
	if len(sorted) == 0 {
		return nil
	}
	if log == nil {
		log = zap.NewNop()
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	initialized := make([]Service, 0, len(sorted))
	var runnersDone <-chan error
	defer func() {
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), opts.ShutdownTimeout)
		defer shutdownCancel()
		cleanup := make(chan error, 1)
		go func() {
			var errs error
			for i := len(initialized) - 1; i >= 0; i-- {
				s := initialized[i]
				if err := s.Stop(shutdownCtx); err != nil {
					errs = errors.Join(errs, fmt.Errorf("stop %s: %w", s.Name(), err))
				}
			}
			cleanup <- errs
		}()
		select {
		case err := <-cleanup:
			result = errors.Join(result, err)
		case <-shutdownCtx.Done():
			result = errors.Join(result, fmt.Errorf("shutdown: %w", shutdownCtx.Err()))
			return
		}
		if runnersDone != nil {
			select {
			case err := <-runnersDone:
				if err != nil && !errors.Is(err, context.Canceled) {
					result = errors.Join(result, err)
				}
			case <-shutdownCtx.Done():
				result = errors.Join(result, fmt.Errorf("wait for services: %w", shutdownCtx.Err()))
			}
		}
	}()
	for _, s := range sorted {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Include a failed Init: it may have acquired only some of its resources.
		initialized = append(initialized, s)
		if err := s.Init(ctx); err != nil {
			return fmt.Errorf("init %s: %w", s.Name(), err)
		}
		if err := s.HealthCheck(ctx); err != nil {
			return fmt.Errorf("health %s: %w", s.Name(), err)
		}
		log.Debug("component initialized", zap.String("component", s.Name()))
	}
	var group errgroup.Group
	for _, s := range sorted {
		group.Go(func() error {
			if err := s.Run(ctx); err != nil {
				cancel()
				return fmt.Errorf("run %s: %w", s.Name(), err)
			}
			return nil
		})
	}
	done := make(chan error, 1)
	runnersDone = done
	go func() { done <- group.Wait() }()
	select {
	case <-ctx.Done():
	case err := <-done:
		runnersDone = nil
		if err != nil {
			return err
		}
		// Resource-only components may return immediately; remain alive until cancellation.
		<-ctx.Done()
	}
	return nil
}

func sortTopologically(services []Service) ([]Service, error) {
	serviceMap := make(map[string]Service)
	inDegree := make(map[string]int)
	adjList := make(map[string][]string)

	for _, s := range services {
		name := s.Name()
		if _, exists := serviceMap[name]; exists {
			return nil, fmt.Errorf("duplicate service name: %s", name)
		}
		serviceMap[name] = s
		inDegree[name] = 0
	}

	for _, s := range services {
		name := s.Name()
		for _, dep := range s.DependsOn() {
			if _, exists := serviceMap[dep]; !exists {
				return nil, fmt.Errorf("service '%s' depends on unknown service '%s'", name, dep)
			}
			adjList[dep] = append(adjList[dep], name)
			inDegree[name]++
		}
	}

	var queue []string
	for _, service := range services {
		name := service.Name()
		if inDegree[name] == 0 {
			queue = append(queue, name)
		}
	}

	var sorted []Service

	for len(queue) > 0 {
		currName := queue[0]
		queue = queue[1:]

		sorted = append(sorted, serviceMap[currName])

		for _, dependentName := range adjList[currName] {
			inDegree[dependentName]--
			if inDegree[dependentName] == 0 {
				queue = append(queue, dependentName)
			}
		}
	}

	if len(sorted) != len(services) {
		return nil, fmt.Errorf("cyclic dependency detected among services")
	}

	return sorted, nil
}
