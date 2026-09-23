// Команда animals-mcp — MCP-сервер источников справочника: шесть
// инструментов русской Википедии и GBIF, те же, что приложение вызывает в
// процессе, плюс служебный server_info со счётчиками вызовов.
//
// Сервер говорит по протоколу MCP через стандартный ввод-вывод: клиент
// запускает его дочерним процессом, пишет запросы в stdin и читает ответы
// из stdout. Приложение запускает его само, когда в диалоге включён
// механизм mcp; вручную его удобно смотреть клиентом mcp-list:
//
//	go run ./cmd/mcp-list
//
// stdout занят протоколом, поэтому всё человекочитаемое уходит в stderr:
// ошибки — всегда, журнал вызовов — с флагом -v.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/AlexS8332/AnimalGuide/internal/mcp"
	"github.com/AlexS8332/AnimalGuide/internal/tools"
)

func main() {
	verbose := flag.Bool("v", false, "писать журнал вызовов инструментов в stderr")
	wikiBase := flag.String("wiki-base", os.Getenv("WIKIPEDIA_BASE_URL"), "адрес API Википедии; пусто — ru.wikipedia.org (переменная WIKIPEDIA_BASE_URL)")
	gbifBase := flag.String("gbif-base", os.Getenv("GBIF_BASE_URL"), "адрес API GBIF; пусто — api.gbif.org (переменная GBIF_BASE_URL)")
	flag.Parse()

	level := slog.LevelWarn
	if *verbose {
		level = slog.LevelInfo
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	// Кэш источников у сервера свой: это второй кэш рядом с кэшем
	// приложения, и он — часть цены механизма.
	ts := tools.LocalTools(tools.NewFetcher(), *wikiBase, *gbifBase)
	srv := mcp.NewServer(ts, mcp.ServerOptions{WikiBase: *wikiBase, GBIFBase: *gbifBase, Logger: logger})

	// Ctrl+C закрывает соединение так же, как закрытый клиентом stdin.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	logger.Info("сервер запущен", "transport", "stdio", "version", mcp.Version, "tools", len(ts))
	if err := srv.Run(ctx, &sdk.StdioTransport{}); err != nil && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, "сервер остановлен с ошибкой:", err)
		os.Exit(1)
	}
}
