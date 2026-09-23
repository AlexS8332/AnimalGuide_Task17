// AnimalGuide — справочник по животным, с которым разговаривают: локальный
// сервер с веб-интерфейсом. Пользователь спрашивает про животное обычными
// словами и получает карточку, собранную только из источников (русская
// Википедия и GBIF), раскрывает её разделы, ходит по дереву классификации,
// сравнивает животных и собирает подборки.
package main

import (
	"context"
	"embed"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/AlexS8332/AnimalGuide/internal/agent"
	"github.com/AlexS8332/AnimalGuide/internal/agents"
	"github.com/AlexS8332/AnimalGuide/internal/charter"
	"github.com/AlexS8332/AnimalGuide/internal/collection"
	"github.com/AlexS8332/AnimalGuide/internal/compiler"
	"github.com/AlexS8332/AnimalGuide/internal/extract"
	"github.com/AlexS8332/AnimalGuide/internal/features"
	"github.com/AlexS8332/AnimalGuide/internal/history"
	"github.com/AlexS8332/AnimalGuide/internal/invariants"
	"github.com/AlexS8332/AnimalGuide/internal/llm"
	"github.com/AlexS8332/AnimalGuide/internal/memory"
	"github.com/AlexS8332/AnimalGuide/internal/persona"
	"github.com/AlexS8332/AnimalGuide/internal/profile"
	"github.com/AlexS8332/AnimalGuide/internal/runs"
	"github.com/AlexS8332/AnimalGuide/internal/server"
	"github.com/AlexS8332/AnimalGuide/internal/store"
	"github.com/AlexS8332/AnimalGuide/internal/tokens"
	"github.com/AlexS8332/AnimalGuide/internal/tools"
)

// Фронтенд лежит в бинарнике: после `go build` приложение запускается одним
// файлом, без каталога с ассетами рядом.
//
//go:embed web
var webFiles embed.FS

const (
	// Слушаем только петлевой интерфейс: приложение локальное.
	defaultAddr = "127.0.0.1:8770"
	// Общий срок одного хода: подборка — это десятки запросов.
	turnTimeout = 10 * time.Minute
	// defaultContextLimit — свой лимит контекста: у DeepSeek в отказе API
	// стоит 1048576 (1 Mi), изменить его нельзя — max_tokens ограничивает
	// только ответ. Поэтому лимит живёт здесь и ловит переполнение до
	// отправки.
	defaultContextLimit = 1_048_576
)

// options — флаги запуска.
type options struct {
	addr, data, featureSpec, overflow string
	open                              bool
	window, keep, limit               int
}

func parseFlags() options {
	var o options
	flag.StringVar(&o.addr, "addr", defaultAddr, "адрес, на котором слушать")
	flag.BoolVar(&o.open, "open", true, "открыть браузер при старте")
	flag.StringVar(&o.data, "data", ".", "каталог данных: history, memory, profiles, collections, invariants")
	flag.StringVar(&o.featureSpec, "features", "", "механизмы новых диалогов поверх умолчаний: «+mcp,-guard», «none,charter», «all»")
	flag.IntVar(&o.window, "window", history.DefaultWindow, "сколько последних сообщений уходит модели дословно (механизм window)")
	flag.IntVar(&o.keep, "keep-tools", history.DefaultKeepToolRunes, "до скольких символов сокращать ответы инструментов прошлых ходов (механизм compact)")
	flag.IntVar(&o.limit, "context-limit", defaultContextLimit, "свой лимит контекста в токенах; 0 — не проверять")
	flag.StringVar(&o.overflow, "on-overflow", agent.OverflowFail, "что делать при переполнении: fail — не отправлять, trim — выбрасывать старые ходы, off — отправить как есть")
	flag.Parse()
	return o
}

func main() {
	o := parseFlags()
	enableUTF8Console()
	loadEnvFiles()

	switch o.overflow {
	case agent.OverflowFail, agent.OverflowTrim, agent.OverflowOff:
	default:
		fail(fmt.Errorf("неизвестный режим -on-overflow=%q; допустимы fail, trim, off", o.overflow))
	}
	registry := features.Catalog()
	defaults, err := registry.Parse(o.featureSpec, registry.Defaults())
	if err != nil {
		fail(err)
	}
	if err := registry.Validate(defaults); err != nil {
		fail(err)
	}

	apiKey := strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	if apiKey == "" {
		fail(fmt.Errorf("не задан DEEPSEEK_API_KEY — задай переменную окружения или впиши ключ в .env.local (см. .env.example)"))
	}
	model := strings.TrimSpace(os.Getenv("DEEPSEEK_MODEL"))
	if model == "" {
		model = llm.DefaultModel
	}

	fetcher := tools.NewFetcher()
	local := tools.MustRegistry(tools.LocalTools(fetcher, os.Getenv("WIKIPEDIA_BASE_URL"), os.Getenv("GBIF_BASE_URL"))...)
	runner := agent.Runner{
		LLM: llm.NewClient(apiKey, os.Getenv("DEEPSEEK_BASE_URL")), Model: model, Temperature: 0,
		ContextLimit: o.limit, OnOverflow: o.overflow, Calibration: &tokens.Calibration{},
	}
	data := store.NewDir(o.data)
	people := &persona.Hook{
		Memory:    memory.NewStore(data),
		Profiles:  profile.NewStore(data),
		Extractor: extract.Extractor{LLM: runner.LLM, Model: model},
	}
	deps := agents.Deps{Runner: runner, Features: registry, Sources: agents.Local{Registry: local}}
	compile := &compiler.Hook{Agents: deps, Store: collection.NewStore(data)}
	// Свод лежит на диске с первого запуска: его читают и правят и без
	// приложения. Судья — тот же клиент и та же модель, без инструментов.
	rules := invariants.NewStore(data)
	if _, err := rules.Ensure(invariants.GuideID); err != nil {
		fail(fmt.Errorf("свод справочника: %w", err))
	}
	guide := &charter.Hook{Store: rules, Judge: invariants.Judge{LLM: runner.LLM, Model: model}}
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
	loaded, problems := manager.Load()
	for _, p := range problems {
		fmt.Fprintln(os.Stderr, "предупреждение: файл диалога пропущен: "+p.Error())
	}

	static, err := fs.Sub(webFiles, "web")
	if err != nil {
		fail(fmt.Errorf("встроенный фронтенд не читается: %w", err))
	}
	meta := map[string]any{"model": model, "window": o.window, "contextLimit": o.limit}
	for _, m := range []map[string]any{persona.Meta(), charter.Meta()} {
		for k, v := range m {
			meta[k] = v
		}
	}
	exts := append(people.Extension(), compile.Extension(manager)...)
	exts = append(exts, guide.Extension()...)
	handler := server.New(manager, static, meta, exts...)

	listener, err := net.Listen("tcp", o.addr)
	if err != nil {
		fail(fmt.Errorf("не удалось занять %s: %w", o.addr, err))
	}
	url := "http://" + listener.Addr().String()
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 10 * time.Second}

	fmt.Println("AnimalGuide — справочник по животным, с которым разговаривают")
	fmt.Println("  интерфейс:  " + url)
	fmt.Println("  модель:     " + model)
	fmt.Println("  источники:  " + strings.Join(local.Names(), ", "))
	fmt.Printf("  диалоги:    %s (загружено: %d)\n", manager.DisplayDir(), loaded)
	fmt.Println("  свод:       " + rules.DisplayPath(invariants.GuideID))
	fmt.Println("  механизмы:  " + defaults.String())
	fmt.Println("  остановить: Ctrl+C")

	errs := make(chan error, 1)
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			errs <- err
		}
	}()
	if o.open {
		openBrowser(url)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	select {
	case err := <-errs:
		fail(err)
	case <-signals:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	fmt.Println("Остановлено. Диалоги остались в " + manager.DisplayDir())
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "Ошибка: "+err.Error())
	os.Exit(1)
}
