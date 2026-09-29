// Command scenic-grid turns the green and water areas staged by the routing data fetch into
// per-city H3 layers the optimizer reads at start.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/andres1m/impuls-goroda/services/optimizer/internal/scenic"
)

func main() {
	data := flag.String("data", "/data", "routing data volume")
	rules := flag.String("rules", "/etc/osrm/scenic.conf", "tag rules of green and water areas")
	flag.Parse()
	if err := run(*data, *rules, log.Default()); err != nil {
		log.Fatal(err)
	}
}

// run builds the layers of the staged version; a city whose layer exists is left as it is.
func run(data, rulesPath string, logger *log.Logger) (retErr error) {
	version, err := os.ReadFile(filepath.Join(data, "next"))
	if err != nil {
		return fmt.Errorf("read staged version: %w", err)
	}
	dir := filepath.Join(data, strings.TrimSpace(string(version)), "scenic")
	rules, err := loadRules(rulesPath)
	if err != nil {
		return err
	}
	staged, err := filepath.Glob(filepath.Join(dir, "*.geojsonseq"))
	if err != nil {
		return fmt.Errorf("glob staged features: %w", err)
	}
	built, err := filepath.Glob(filepath.Join(dir, "*.csv"))
	if err != nil {
		return fmt.Errorf("glob built layers: %w", err)
	}
	if len(staged) == 0 && len(built) == 0 {
		return fmt.Errorf("no scenic features staged in %s", dir)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open scenic dir: %w", err)
	}
	defer func() {
		retErr = errors.Join(retErr, root.Close())
	}()
	for _, features := range staged {
		if err := buildCity(root, filepath.Base(features), rules, logger); err != nil {
			return err
		}
	}
	return nil
}

func loadRules(rulesPath string) (rules scenic.Rules, retErr error) {
	rulesFile, err := os.Open(rulesPath)
	if err != nil {
		return scenic.Rules{}, fmt.Errorf("open rules: %w", err)
	}
	defer func() {
		retErr = errors.Join(retErr, rulesFile.Close())
	}()
	return scenic.ParseRules(rulesFile)
}

func buildCity(root *os.Root, featuresFile string, rules scenic.Rules, logger *log.Logger) error {
	city := strings.TrimSuffix(featuresFile, ".geojsonseq")
	layer := city + ".csv"
	_, statErr := root.Stat(layer)
	switch {
	case errors.Is(statErr, os.ErrNotExist):
		cells, err := build(root, featuresFile, layer, rules)
		if err != nil {
			return fmt.Errorf("%s: %w", city, err)
		}
		logger.Printf("scenic layer of %s: %d cells", city, cells)
	case statErr != nil:
		return fmt.Errorf("stat layer %s: %w", layer, statErr)
	}
	if err := root.Remove(featuresFile); err != nil {
		return fmt.Errorf("remove staged features %s: %w", featuresFile, err)
	}
	return nil
}

// build writes the layer through a temporary file, so a failed run never leaves a partial one.
func build(root *os.Root, features, layer string, rules scenic.Rules) (cells int, retErr error) {
	in, err := root.Open(features)
	if err != nil {
		return 0, fmt.Errorf("open features: %w", err)
	}
	defer func() {
		retErr = errors.Join(retErr, in.Close())
	}()
	shares, err := scenic.Cover(in, rules)
	if err != nil {
		return 0, err
	}
	tmp := layer + ".tmp"
	if err := writeLayerFile(root, tmp, shares); err != nil {
		return 0, err
	}
	if err := root.Rename(tmp, layer); err != nil {
		return 0, fmt.Errorf("rename layer: %w", err)
	}
	return len(shares), nil
}

func writeLayerFile(root *os.Root, tmp string, shares scenic.Shares) (retErr error) {
	out, err := root.Create(tmp)
	if err != nil {
		return fmt.Errorf("create temp layer: %w", err)
	}
	defer func() {
		if closeErr := out.Close(); closeErr != nil && retErr == nil {
			retErr = fmt.Errorf("close temp layer: %w", closeErr)
		}
	}()
	return scenic.WriteLayer(out, shares)
}
