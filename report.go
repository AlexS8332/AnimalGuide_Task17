package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/AlexS8332/AnimalGuide/internal/agent"
	"github.com/AlexS8332/AnimalGuide/internal/bench"
	"github.com/AlexS8332/AnimalGuide/internal/features"
	"github.com/AlexS8332/AnimalGuide/internal/runs"
)

// legacyDir — памятники прошлых форматов: проверка совместимости И-6.
// Путь от корня репозитория: -report запускается оттуда.
const legacyDir = "testdata/legacy"

// runReport — опыт -report: все выбранные испытания на живой модели, отчёт
// в markdown. Каждое испытание работает в своём каталоге внутри
// <data>/bench/<время>: диалоги, профили и подборки прогона остаются там,
// и по ним можно пройти за любым числом отчёта.
func runReport(o options, registry *features.Registry, defaults features.Set, runner agent.Runner, model string) error {
	trials, err := bench.Select(bench.Trials(), o.trials)
	if err != nil {
		return err
	}
	root := filepath.Join(o.data, "bench", time.Now().Format("20060102-150405"))
	env := &bench.Env{
		Registry: registry, Base: defaults, Root: root, Legacy: legacyDir, Model: model,
		Timeout: turnTimeout, Progress: os.Stdout,
		Open: func(dir string, bo bench.Options) (*runs.Manager, error) {
			return wire(o, registry, defaults, runner, dir, bo.WikiBase).Manager, nil
		},
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fmt.Printf("Опыт -report: испытаний %d, модель %s, каталог прогона %s\n", len(trials), model, root)
	started := time.Now()
	results := bench.Run(ctx, env, trials)
	md := bench.Markdown(bench.Meta{Model: model, Started: started, Elapsed: time.Since(started), Registry: registry}, results)
	if err := os.MkdirAll(filepath.Dir(o.reportOut), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(o.reportOut, []byte(md), 0o644); err != nil {
		return err
	}
	fmt.Println()
	for _, r := range results {
		fmt.Printf("  %s %-40s проверок %d из %d\n", r.ID, r.Title, r.Count(bench.Pass), len(r.Checks))
	}
	fmt.Println("Отчёт: " + o.reportOut)
	return nil
}
