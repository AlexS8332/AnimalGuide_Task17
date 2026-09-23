package main

import (
	"os"

	"github.com/AlexS8332/AnimalGuide/internal/agent"
	"github.com/AlexS8332/AnimalGuide/internal/agents"
	"github.com/AlexS8332/AnimalGuide/internal/collection"
	"github.com/AlexS8332/AnimalGuide/internal/compiler"
	"github.com/AlexS8332/AnimalGuide/internal/extract"
	"github.com/AlexS8332/AnimalGuide/internal/features"
	"github.com/AlexS8332/AnimalGuide/internal/history"
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
	Local   *tools.Registry
}

// wire собирает менеджер ходов: источники, агенты и механизмы вокруг хода.
// Им пользуются и сервер, и стенд -report: стенд обязан гонять ровно то,
// что увидит человек, поэтому сборка одна. wikiBase — адрес Википедии
// вместо заданного окружением (подставные статьи стенда); пусто — как есть.
func wire(o options, registry *features.Registry, defaults features.Set, runner agent.Runner, dataDir, wikiBase string) app {
	if wikiBase == "" {
		wikiBase = os.Getenv("WIKIPEDIA_BASE_URL")
	}
	local := tools.MustRegistry(tools.LocalTools(tools.NewFetcher(), wikiBase, os.Getenv("GBIF_BASE_URL"))...)
	data := store.NewDir(dataDir)
	people := &persona.Hook{
		Memory:    memory.NewStore(data),
		Profiles:  profile.NewStore(data),
		Extractor: extract.Extractor{LLM: runner.LLM, Model: runner.Model},
	}
	deps := agents.Deps{Runner: runner, Features: registry, Sources: agents.Local{Registry: local}}
	compile := &compiler.Hook{Agents: deps, Store: collection.NewStore(data)}
	manager := runs.NewManager(runs.Config{
		Agents:   deps,
		Store:    history.NewStore(data),
		Registry: registry, Defaults: defaults, Timeout: turnTimeout,
		Window: o.window, KeepToolRunes: o.keep,
		// Составитель раньше человека: его ход видит блоки профиля и памяти.
		Hooks: []runs.Hook{compile, people},
	})
	return app{Manager: manager, People: people, Compile: compile, Local: local}
}
