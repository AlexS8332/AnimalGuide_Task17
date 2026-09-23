// AnimalGuide — справочник по животным, с которым разговаривают: локальный
// сервер с веб-интерфейсом. Пользователь спрашивает про животное обычными
// словами и получает карточку, собранную только из источников (русская
// Википедия и GBIF), раскрывает её разделы, ходит по дереву классификации,
// сравнивает животных и собирает подборки.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/AlexS8332/AnimalGuide/internal/features"
	"github.com/AlexS8332/AnimalGuide/internal/llm"
)

// Фронтенд лежит в бинарнике: после `go build` приложение запускается одним
// файлом, без каталога с ассетами рядом.
//
//go:embed web
var webFiles embed.FS

// Слушаем только петлевой интерфейс: приложение локальное.
const defaultAddr = "127.0.0.1:8770"

func main() {
	addr := flag.String("addr", defaultAddr, "адрес, на котором слушать")
	open := flag.Bool("open", true, "открыть браузер при старте")
	featureSpec := flag.String("features", "", "механизмы новых диалогов поверх умолчаний: «+mcp,-guard», «none,charter», «all»")
	flag.Parse()

	enableUTF8Console()
	loadEnvFiles()

	registry := features.Catalog()
	set, err := registry.Parse(*featureSpec, registry.Defaults())
	if err != nil {
		fail(err)
	}
	if err := registry.Validate(set); err != nil {
		fail(err)
	}

	if strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")) == "" {
		fail(fmt.Errorf("не задан DEEPSEEK_API_KEY — задай переменную окружения или впиши ключ в .env.local (см. .env.example)"))
	}
	model := strings.TrimSpace(os.Getenv("DEEPSEEK_MODEL"))
	if model == "" {
		model = llm.DefaultModel
	}

	static, err := fs.Sub(webFiles, "web")
	if err != nil {
		fail(fmt.Errorf("встроенный фронтенд не читается: %w", err))
	}
	mux := http.NewServeMux()
	mux.Handle("/", http.FileServer(http.FS(static)))
	mux.HandleFunc("/api/features", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		json.NewEncoder(w).Encode(map[string]any{"mechanisms": registry.Describe(set), "model": model})
	})

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		fail(fmt.Errorf("не удалось занять %s: %w", *addr, err))
	}
	url := "http://" + listener.Addr().String()
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	fmt.Println("AnimalGuide — справочник по животным")
	fmt.Println("  интерфейс:  " + url)
	fmt.Println("  модель:     " + model)
	fmt.Println("  механизмы:  " + set.String())
	fmt.Println("  остановить: Ctrl+C")

	errs := make(chan error, 1)
	go func() {
		if err := srv.Serve(listener); err != nil && err != http.ErrServerClosed {
			errs <- err
		}
	}()
	if *open {
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
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "Ошибка: "+err.Error())
	os.Exit(1)
}
