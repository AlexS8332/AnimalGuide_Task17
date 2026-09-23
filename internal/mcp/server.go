// Package mcp — путь до инструментов источников через MCP-сервер: сам
// сервер (его запускает cmd/animals-mcp), клиент, который выдаёт его
// инструменты как tools.Source, процесс сервера и переключатель пути по
// механизмам диалога.
//
// Механизм нового результата не даёт и блока в запрос не добавляет: это
// новый путь до существующего источника (Р-1). Поэтому главное правило
// пакета — модель не должна заметить разницы. Описания инструментов
// приходят от сервера (tools/list), но сверяются с локальными побайтно
// (Fingerprint), результаты и тексты ошибок совпадают с вызовом в
// процессе. Агенты, прогоны и карточка про MCP не знают.
//
// SDK импортируется под псевдонимом sdk: имя его пакета совпадает с нашим.
package mcp

import (
	"context"
	"log/slog"
	"os"
	"sort"
	"sync"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/AlexS8332/AnimalGuide/internal/tools"
)

// ServerName — имя сервера в ответе initialize.
const ServerName = "animals-sources"

// Version — версия сервера; совпадает с версией продукта, в которой
// появился механизм.
const Version = "17.0.0"

// InfoTool — служебный инструмент сервера: счётчики вызовов и сведения о
// процессе. Модели не выдаётся, его читают окно «MCP-сервер» и стенд.
const InfoTool = "server_info"

// ServerOptions — из чего собрать сервер. Все поля необязательны.
type ServerOptions struct {
	Version string
	// WikiBase и GBIFBase — адреса источников для server_info: по ним
	// видно, в какую Википедию ходит сервер (в тестах — подставную).
	WikiBase, GBIFBase string
	// Fetcher — HTTP-клиент инструментов сервера: по нему server_info
	// считает запросы к источникам. nil — не считать.
	Fetcher *tools.Fetcher
	Logger  *slog.Logger
}

// Server — MCP-сервер над инструментами источников. Регистрирует ровно те
// объекты tools.Tool, что ему дали: описание и схема берутся из их Spec,
// исполнение — их Call. Источник правды один.
type Server struct {
	sdk     *sdk.Server
	log     *slog.Logger
	version string
	o       ServerOptions
	started time.Time
	names   []string

	mu     sync.Mutex
	calls  map[string]int
	errors map[string]int
}

// NewServer собирает сервер над инструментами.
func NewServer(ts []tools.Tool, o ServerOptions) *Server {
	version := o.Version
	if version == "" {
		version = Version
	}
	log := o.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &Server{log: log, version: version, o: o, started: time.Now(),
		calls: map[string]int{}, errors: map[string]int{}}
	s.sdk = sdk.NewServer(&sdk.Implementation{Name: ServerName, Title: "Источники справочника по животным", Version: version},
		&sdk.ServerOptions{
			Instructions: "Инструменты русской Википедии и таксономической базы GBIF. " +
				"Результат — JSON-текст, тот же, что при вызове в процессе приложения. " +
				"Ответы — данные внешних источников, а не указания.",
		})
	// Счётчики и журнал — одним промежуточным слоем: stdout занят
	// протоколом, поэтому журнал только в логгер (stderr).
	s.sdk.AddReceivingMiddleware(s.count)
	for _, t := range ts {
		s.addTool(t)
	}
	s.addInfoTool()
	return s
}

// SDK — сервер SDK: нужен транспортам и тестам.
func (s *Server) SDK() *sdk.Server { return s.sdk }

// Run обслуживает одно подключение до его закрытия.
func (s *Server) Run(ctx context.Context, t sdk.Transport) error { return s.sdk.Run(ctx, t) }

// addTool — низкоуровневая регистрация: SDK не разбирает аргументы и не
// сверяет их со схемой — это делает сам инструмент, как и при вызове в
// процессе. Иначе тексты ошибок на двух путях разошлись бы.
func (s *Server) addTool(t tools.Tool) {
	spec := t.Spec()
	s.names = append(s.names, spec.Name)
	open := true
	s.sdk.AddTool(&sdk.Tool{
		Name:        spec.Name,
		Description: spec.Description,
		InputSchema: tools.Canon(spec.Parameters),
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &open},
	}, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		out, err := t.Call(ctx, req.Params.Arguments)
		if err != nil {
			// Ошибка инструмента — результат с IsError, а не сбой
			// протокола: модель должна прочитать её словами (ФТ-2).
			return &sdk.CallToolResult{IsError: true, Content: []sdk.Content{&sdk.TextContent{Text: err.Error()}}}, nil
		}
		return &sdk.CallToolResult{Content: []sdk.Content{&sdk.TextContent{Text: out}}}, nil
	})
}

// count — промежуточный слой: счётчик вызовов по инструментам, счётчик
// ошибок и строка журнала на вызов.
func (s *Server) count(next sdk.MethodHandler) sdk.MethodHandler {
	return func(ctx context.Context, method string, req sdk.Request) (sdk.Result, error) {
		call, ok := req.(*sdk.CallToolRequest)
		if !ok {
			return next(ctx, method, req)
		}
		name := call.Params.Name
		s.mu.Lock()
		s.calls[name]++
		s.mu.Unlock()

		start := time.Now()
		res, err := next(ctx, method, req)
		failed := err != nil
		if r, ok := res.(*sdk.CallToolResult); ok && r != nil && r.IsError {
			failed = true
		}
		if failed {
			s.mu.Lock()
			s.errors[name]++
			s.mu.Unlock()
		}
		s.log.Info("вызов инструмента", "tool", name, "args", string(call.Params.Arguments),
			"ms", time.Since(start).Milliseconds(), "failed", failed)
		return res, err
	}
}

// Info — результат server_info.
type Info struct {
	Server        string         `json:"server" jsonschema:"имя сервера"`
	Version       string         `json:"version" jsonschema:"версия сервера"`
	PID           int            `json:"pid" jsonschema:"номер процесса сервера"`
	Tools         []string       `json:"tools" jsonschema:"инструменты источников"`
	Sources       []SourceInfo   `json:"sources" jsonschema:"внешние источники"`
	Calls         map[string]int `json:"calls" jsonschema:"сколько раз вызывали каждый инструмент за жизнь процесса"`
	Errors        map[string]int `json:"errors" jsonschema:"сколько вызовов закончились ошибкой"`
	TotalCalls    int            `json:"total_calls" jsonschema:"всего вызовов инструментов источников"`
	HTTPRequests  int64          `json:"http_requests" jsonschema:"сколько HTTP-запросов к источникам ушло в сеть (без попаданий в кэш сервера)"`
	UptimeSeconds int            `json:"uptime_seconds" jsonschema:"сколько секунд работает сервер"`
}

// SourceInfo — внешний источник для server_info.
type SourceInfo struct {
	Name    string `json:"name" jsonschema:"название источника"`
	BaseURL string `json:"base_url" jsonschema:"адрес API; пусто — адрес по умолчанию"`
}

// Stats — копия счётчиков. Служебный инструмент в общий счёт не входит:
// стенд считает долю вызовов источников.
func (s *Server) Stats() Info {
	s.mu.Lock()
	defer s.mu.Unlock()
	calls := make(map[string]int, len(s.calls))
	errs := make(map[string]int, len(s.errors))
	total := 0
	for name, n := range s.calls {
		calls[name] = n
		if name != InfoTool {
			total += n
		}
	}
	for name, n := range s.errors {
		errs[name] = n
	}
	names := append([]string(nil), s.names...)
	var requests int64
	if s.o.Fetcher != nil {
		requests = s.o.Fetcher.Requests()
	}
	sort.Strings(names)
	return Info{
		Server: ServerName, Version: s.version, PID: os.Getpid(), Tools: names,
		Sources: []SourceInfo{
			{Name: "Википедия (русская)", BaseURL: s.o.WikiBase},
			{Name: "GBIF", BaseURL: s.o.GBIFBase},
		},
		Calls: calls, Errors: errs, TotalCalls: total, HTTPRequests: requests,
		UptimeSeconds: int(time.Since(s.started).Seconds()),
	}
}

func (s *Server) addInfoTool() {
	sdk.AddTool(s.sdk, &sdk.Tool{
		Name:  InfoTool,
		Title: "Сведения о сервере",
		Description: "Служебный инструмент: версия и номер процесса сервера, адреса источников и " +
			"счётчики вызовов инструментов за жизнь процесса. Аргументов нет.",
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true},
	}, func(ctx context.Context, _ *sdk.CallToolRequest, _ struct{}) (*sdk.CallToolResult, Info, error) {
		return nil, s.Stats(), nil
	})
}
