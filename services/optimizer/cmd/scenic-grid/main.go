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
func run(data, rulesPath string, logger *log.Logger) error {
	version, err := os.ReadFile(filepath.Join(data, "next"))
	if err != nil {
		return err
	}
	dir := filepath.Join(data, strings.TrimSpace(string(version)), "scenic")
	rulesFile, err := os.Open(rulesPath)
	if err != nil {
		return err
	}
	rules, err := scenic.ParseRules(rulesFile)
	rulesFile.Close()
	if err != nil {
		return err
	}
	staged, err := filepath.Glob(filepath.Join(dir, "*.geojsonseq"))
	if err != nil {
		return err
	}
	built, err := filepath.Glob(filepath.Join(dir, "*.csv"))
	if err != nil {
		return err
	}
	if len(staged) == 0 && len(built) == 0 {
		return fmt.Errorf("no scenic features staged in %s", dir)
	}
	for _, features := range staged {
		city := strings.TrimSuffix(filepath.Base(features), ".geojsonseq")
		layer := filepath.Join(dir, city+".csv")
		if _, err := os.Stat(layer); errors.Is(err, os.ErrNotExist) {
			cells, err := build(features, layer, rules)
			if err != nil {
				return fmt.Errorf("%s: %w", city, err)
			}
			logger.Printf("scenic layer of %s: %d cells", city, cells)
		} else if err != nil {
			return err
		}
		if err := os.Remove(features); err != nil {
			return err
		}
	}
	return nil
}

// build writes the layer through a temporary file, so a failed run never leaves a partial one.
func build(features, layer string, rules scenic.Rules) (int, error) {
	in, err := os.Open(features)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	shares, err := scenic.Cover(in, rules)
	if err != nil {
		return 0, err
	}
	tmp := layer + ".tmp"
	out, err := os.Create(tmp)
	if err != nil {
		return 0, err
	}
	if err := scenic.WriteLayer(out, shares); err != nil {
		out.Close()
		return 0, err
	}
	if err := out.Close(); err != nil {
		return 0, err
	}
	return len(shares), os.Rename(tmp, layer)
}
