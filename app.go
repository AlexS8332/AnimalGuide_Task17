package main

import (
	"fmt"
	"os"

	"github.com/AlexS8332/AnimalGuide/internal/agent"
	"github.com/AlexS8332/AnimalGuide/internal/agents"
	"github.com/AlexS8332/AnimalGuide/internal/charter"
	"github.com/AlexS8332/AnimalGuide/internal/collection"
	"github.com/AlexS8332/AnimalGuide/internal/compiler"
	"github.com/AlexS8332/AnimalGuide/internal/extract"
	"github.com/AlexS8332/AnimalGuide/internal/features"
	"github.com/AlexS8332/AnimalGuide/internal/history"
	"github.com/AlexS8332/AnimalGuide/internal/invariants"
	"github.com/AlexS8332/AnimalGuide/internal/mcp"
	"github.com/AlexS8332/AnimalGuide/internal/memory"
	"github.com/AlexS8332/AnimalGuide/internal/persona"
	"github.com/AlexS8332/AnimalGuide/internal/profile"
	"github.com/AlexS8332/AnimalGuide/internal/runs"
	"github.com/AlexS8332/AnimalGuide/internal/store"
	"github.com/AlexS8332/AnimalGuide/internal/tools"
)

// app — собранное приложение над одним каталогом данных.
type app struct {
	Manager *runs.Manager
	People  *persona.Hook
	Compile *compiler.Hook
	Guide   *charter.Hook
	Local   *tools.Registry
	// Fetcher — HTTP-клиент источников в процессе: стенд считает по нему
	// запросы в сеть.
	Fetcher *tools.Fetcher
	// Sources — путь до источников на ход: в процессе или через MCP-сервер.
	Sources *mcp.Switch
	// Close гасит клиент и процесс MCP-сервера, если он запускался.
	Close func()
}

// wire собирает менеджер ходов: источники, агенты и механизмы вокруг хода.
// Им пользуются и сервер, и стенд -report: стенд обязан гонять ровно то,
// что увидит человек, поэтому сборка одна. wikiBase — адрес Википедии
// вместо заданного окружением (подставные статьи стенда); пусто — как есть.
func wire(o options, registry *features.Registry, defaults features.Set, runner agent.Runner, dataDir, wikiBase string) (app, error) {
	if wikiBase == "" {
		wikiBase = os.Getenv("WIKIPEDIA_BASE_URL")
	}
	fetcher := tools.NewFetcher()
	localTools := tools.LocalTools(fetcher, wikiBase, os.Getenv("GBIF_BASE_URL"))
	local := tools.MustRegistry(localTools...)
	// Путь до источников выбирается на каждый ход по механизмам диалога:
	// mcp выключен — вызов в процессе, включён — через MCP-сервер. Процесс
	// сервера запускается при первом ходе с mcp, не раньше; адреса
	// источников у него те же, что у вызова в процессе.
	launcher := &mcp.Launcher{Path: o.mcpServer}
	if wikiBase != "" {
		launcher.Args = append(launcher.Args, "-wiki-base", wikiBase)
	}
	client := mcp.NewClient(mcp.Options{Dial: launcher.Dial, Want: tools.Fingerprint(localTools)})
	sources := &mcp.Switch{Local: local, Client: client, How: launcher.How}
	data := store.NewDir(dataDir)
	people := &persona.Hook{
		Memory:    memory.NewStore(data),
		Profiles:  profile.NewStore(data),
		Extractor: extract.Extractor{LLM: runner.LLM, Model: runner.Model},
	}
	deps := agents.Deps{Runner: runner, Features: registry, Sources: sources}
	compile := &compiler.Hook{Agents: deps, Store: collection.NewStore(data)}
	// Свод лежит на диске с первого запуска: его читают и правят и без
	// приложения. Судья — тот же клиент и та же модель, без инструментов.
	rules := invariants.NewStore(data)
	if _, err := rules.Ensure(invariants.GuideID); err != nil {
		return app{}, fmt.Errorf("свод справочника: %w", err)
	}
	guide := &charter.Hook{Store: rules, Judge: invariants.Judge{LLM: runner.LLM, Model: runner.Model}}
	manager := runs.NewManager(runs.Config{
		Agents:   deps,
		Store:    history.NewStore(data),
		Registry: registry, Defaults: defaults, Timeout: turnTimeout,
		Window: o.window, KeepToolRunes: o.keep,
		// Составитель первым: его ход видит блоки свода, профиля и памяти.
		// Страж свода — раньше человека: соблюдение профиля проверяется по
		// тому ответу, который дойдёт до человека.
		Hooks: []runs.Hook{compile, guide, people},
	})
	return app{Manager: manager, People: people, Compile: compile, Guide: guide, Local: local, Fetcher: fetcher,
		Sources: sources, Close: func() { client.Close(); launcher.Close() }}, nil
}
