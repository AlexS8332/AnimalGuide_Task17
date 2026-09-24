//go:build edge

// Проверки интерфейса в настоящем браузере: headless Microsoft Edge
// открывает встроенный фронтенд, сценарий testdata/edge/scenario.js
// нажимает те же кнопки, что и человек, и пишет итог в <pre id="test-log">,
// а тест снимает DOM (--dump-dom) и разбирает строки OK/FAIL. Модель и
// источники подставные — сети и ключа не нужно.
//
// Запуск: go test -tags edge -run TestEdge -v .
// Снимки экрана: EDGE_SHOTS=каталог go test -tags edge -run TestEdge .
// Путь к браузеру, если он не в стандартном месте: EDGE_PATH.
package main

import (
	"context"
	"fmt"
	"html"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/AlexS8332/AnimalGuide_Task17/internal/agent"
	"github.com/AlexS8332/AnimalGuide_Task17/internal/agents"
	"github.com/AlexS8332/AnimalGuide_Task17/internal/agents/agentstest"
	"github.com/AlexS8332/AnimalGuide_Task17/internal/collection"
	"github.com/AlexS8332/AnimalGuide_Task17/internal/compiler"
	"github.com/AlexS8332/AnimalGuide_Task17/internal/extract"
	"github.com/AlexS8332/AnimalGuide_Task17/internal/features"
	"github.com/AlexS8332/AnimalGuide_Task17/internal/history"
	"github.com/AlexS8332/AnimalGuide_Task17/internal/llm"
	"github.com/AlexS8332/AnimalGuide_Task17/internal/llm/llmtest"
	"github.com/AlexS8332/AnimalGuide_Task17/internal/memory"
	"github.com/AlexS8332/AnimalGuide_Task17/internal/persona"
	"github.com/AlexS8332/AnimalGuide_Task17/internal/profile"
	"github.com/AlexS8332/AnimalGuide_Task17/internal/runs"
	"github.com/AlexS8332/AnimalGuide_Task17/internal/server"
	"github.com/AlexS8332/AnimalGuide_Task17/internal/store"
	"github.com/AlexS8332/AnimalGuide_Task17/internal/tools"
	"github.com/AlexS8332/AnimalGuide_Task17/internal/tools/toolstest"
)

// findEdge — путь к msedge: EDGE_PATH или стандартные места установки.
func findEdge() string {
	if p := os.Getenv("EDGE_PATH"); p != "" {
		return p
	}
	var candidates []string
	switch runtime.GOOS {
	case "windows":
		for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LocalAppData"} {
			if dir := os.Getenv(env); dir != "" {
				candidates = append(candidates, filepath.Join(dir, "Microsoft", "Edge", "Application", "msedge.exe"))
			}
		}
	case "darwin":
		candidates = append(candidates, "/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge")
	default:
		candidates = append(candidates, "microsoft-edge", "microsoft-edge-stable")
	}
	for _, c := range candidates {
		if filepath.IsAbs(c) {
			if _, err := os.Stat(c); err == nil {
				return c
			}
		} else if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}

// Реплика с предпочтениями: извлекатель раскладывает её по профилю, памяти
// и карточке фактов — под ответом появляются чипы.
const edgePrefs = `{"profile":{"set":[{"field":"length","value":"short","scope":"always","quote":"пиши мне всегда коротко"}]},
 "memory":{"set":[{"layer":"long","key":"интерес","value":"хищники тайги"}]},
 "facts":{"set":[{"key":"тема","value":"чем питаются рыси"}]}}`

// edgeCompiler — добросовестный составитель подборки: план, затем по
// утверждению — первый вид.
func edgeCompiler(req llm.Request, step int) llm.Response {
	user := agentstest.LastUser(req)
	switch {
	case strings.Contains(user, "Собери подборку"):
		if step == 0 {
			return llmtest.ToolCall("plan", `{"goal":"доклад о кошках","species":["рысь","манул"]}`)
		}
		return llmtest.Text("План: рысь, манул. Утверждаете?")
	case strings.Contains(user, "утверждаю"):
		steps := []llm.Response{
			llmtest.ToolCall("approve", `{"quote":"план утверждаю"}`),
			llmtest.ToolCall("deliver", `{"n":1}`),
			llmtest.ToolCall("step_done", `{"n":1,"result":"карточка собрана"}`),
		}
		if step < len(steps) {
			return steps[step]
		}
		return llmtest.Text("Первый вид собран.")
	}
	return llmtest.Text("…")
}

// edgeApp — приложение, собранное как в main, но на подставной модели и
// подставных источниках, с данными во временном каталоге.
type edgeApp struct {
	m       *runs.Manager
	handler http.Handler
	// Диалоги сценария: основной (карточка, ветки, сравнение), подборка и
	// пустой.
	main, coll, empty string
}

func newEdgeApp(t *testing.T) *edgeApp {
	t.Helper()
	wiki, gbif := toolstest.NewWiki(), toolstest.NewGBIF()
	t.Cleanup(wiki.Close)
	t.Cleanup(gbif.Close)
	brain := &agentstest.Brain{Compiler: edgeCompiler}
	brain.Extract = func(req llm.Request) (string, error) {
		if strings.Contains(agentstest.LastUser(req), "коротко") {
			return edgePrefs, nil
		}
		return `{"profile":{"set":[]},"memory":{"set":[]},"facts":{"set":[]}}`, nil
	}
	brain.LeadScript = func(llm.Request, int) llm.Response {
		return llmtest.Text("Рысь охотится на зайцев, реже — на косуль и птиц.")
	}
	fake := &llmtest.Fake{Fn: brain.Chat}
	registry := features.Catalog()
	data := store.NewDir(t.TempDir())
	local := tools.MustRegistry(tools.LocalTools(tools.NewFetcher(), wiki.URL, gbif.URL)...)
	deps := agents.Deps{Runner: agent.Runner{LLM: fake, Model: llm.DefaultModel}, Features: registry, Sources: agents.Local{Registry: local}}
	people := &persona.Hook{Memory: memory.NewStore(data), Profiles: profile.NewStore(data),
		Extractor: extract.Extractor{LLM: fake, Model: llm.DefaultModel}}
	compile := &compiler.Hook{Agents: deps, Store: collection.NewStore(data)}
	m := runs.NewManager(runs.Config{
		Agents: deps, Store: history.NewStore(data), Registry: registry, Defaults: registry.Defaults(),
		Timeout: time.Minute, Window: history.DefaultWindow, KeepToolRunes: history.DefaultKeepToolRunes,
		Hooks: []runs.Hook{compile, people},
	})
	static, err := fs.Sub(webFiles, "web")
	if err != nil {
		t.Fatal(err)
	}
	meta := map[string]any{"model": llm.DefaultModel, "window": history.DefaultWindow, "contextLimit": defaultContextLimit}
	for k, v := range persona.Meta() {
		meta[k] = v
	}
	a := &edgeApp{m: m}
	a.handler = server.New(m, static, meta, append(people.Extension(), compile.Extension(m)...)...)
	a.seed(t)
	return a
}

// turn — ход и ожидание его конца; conv пусто — новый диалог.
func (a *edgeApp) turn(t *testing.T, conv string, req agents.Request) runs.Detail {
	t.Helper()
	var s *runs.Session
	var err error
	if conv == "" {
		s, err = a.m.Start(runs.StartOptions{Request: req})
	} else {
		s, err = a.m.Send(conv, req)
	}
	if err != nil {
		t.Fatal(err)
	}
	v := s.Wait(30 * time.Second)
	if v.Status != runs.StatusDone {
		t.Fatalf("ход %+v: %s %s", req, v.Status, v.Error)
	}
	d, _ := a.m.Get(v.ConversationID)
	return d
}

// seed заводит диалоги, по которым ходит сценарий. Порядок важен: список
// диалогов отсортирован по времени, пустой — последним.
func (a *edgeApp) seed(t *testing.T) {
	t.Helper()
	d := a.turn(t, "", agents.Request{Text: "Собери подборку: кошки нашей фауны, два вида"})
	a.coll = d.ID
	a.turn(t, a.coll, agents.Request{Text: "План утверждаю"})

	d = a.turn(t, "", agents.Request{Text: "рысь"})
	a.main = d.ID
	cardID := d.Cards.Cards[0].ID
	a.turn(t, a.main, agents.Request{Kind: "section", CardID: cardID, Topic: "diet"})
	a.turn(t, a.main, agents.Request{Kind: "node", CardID: cardID, NodeKey: toolstest.KeyFelidae, NodeName: "Кошачьи"})
	a.turn(t, a.main, agents.Request{Text: "Пиши мне всегда коротко. Мне интересны хищники тайги: чем питается рысь?"})
	// Сравнение само ставит точку «до сравнения» и уходит в свою ветку.
	a.turn(t, a.main, agents.Request{Kind: "compare", A: "рысь", B: "манул", Text: "Сравни: рысь и манул"})

	e, err := a.m.Create(runs.StartOptions{})
	if err != nil {
		t.Fatal(err)
	}
	a.empty = e.ID
}

// page — сервер для браузера: настоящий обработчик приложения, но в
// index.html после app.js подключён сценарий (и, для снимков, стиль, при
// котором прокручивается body, а не окно).
func (a *edgeApp) page(t *testing.T, shots bool) *httptest.Server {
	t.Helper()
	index, err := fs.ReadFile(webFiles, "web/index.html")
	if err != nil {
		t.Fatal(err)
	}
	scenario, err := os.ReadFile(filepath.Join("testdata", "edge", "scenario.js"))
	if err != nil {
		t.Fatal(err)
	}
	inject := `<script src="/edge-scenario.js"></script>`
	if shots {
		inject = `<style>html{height:100%;overflow:hidden}body{height:100%;overflow:auto}</style>` + inject
	}
	patched := strings.Replace(string(index), "</body>", inject+"\n</body>", 1)
	mux := http.NewServeMux()
	mux.HandleFunc("/edge-scenario.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Write(scenario)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Write([]byte(patched))
			return
		}
		a.handler.ServeHTTP(w, r)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// edgeRun запускает headless Edge; out — --dump-dom или --screenshot=….
func edgeRun(t *testing.T, edge, url string, size string, out ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	args := append([]string{"--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check",
		"--disable-extensions", "--user-data-dir=" + t.TempDir(), "--virtual-time-budget=30000",
		"--window-size=" + size}, out...)
	cmd := exec.CommandContext(ctx, edge, append(args, url)...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("edge: %v\n%s", err, stderr.String())
	}
	return stdout.String()
}

var logRe = regexp.MustCompile(`(?s)<pre id="test-log"[^>]*>(.*?)</pre>`)

// TestEdge — проверки интерфейса по сценариям. Каждая строка OK/FAIL
// сценария — отдельный подтест.
func TestEdge(t *testing.T) {
	edge := findEdge()
	if edge == "" {
		t.Skip("Microsoft Edge не найден (задайте EDGE_PATH)")
	}
	a := newEdgeApp(t)
	srv := a.page(t, false)
	scenarios := []struct{ name, conv string }{
		{"main", a.main},
		{"collection", a.coll},
		{"empty", a.empty},
	}
	total := 0
	for _, sc := range scenarios {
		t.Run(sc.name, func(t *testing.T) {
			dom := edgeRun(t, edge, fmt.Sprintf("%s/?scenario=%s#c=%s", srv.URL, sc.name, sc.conv), "1400,900", "--dump-dom")
			m := logRe.FindStringSubmatch(dom)
			if m == nil {
				t.Fatalf("сценарий не оставил журнала; DOM начинается так:\n%.2000s", dom)
			}
			lines := strings.Split(strings.TrimSpace(html.UnescapeString(m[1])), "\n")
			done := false
			for _, line := range lines {
				status, rest, _ := strings.Cut(line, " ")
				name, reason, _ := strings.Cut(rest, " — ")
				switch status {
				case "OK", "FAIL":
					total++
					t.Run(name, func(t *testing.T) {
						if status == "FAIL" {
							t.Error(reason)
						}
					})
				case "DONE":
					done = true
				default:
					t.Log(line)
				}
			}
			if !done {
				t.Errorf("сценарий не дошёл до конца:\n%s", strings.Join(lines, "\n"))
			}
		})
	}
	if total < 20 {
		t.Errorf("проверок %d, а должно быть не меньше 20", total)
	}

	if dir := os.Getenv("EDGE_SHOTS"); dir != "" {
		// Снимки: body прокручивается сам, иначе headless Edge рисует
		// прокрученную страницу со сдвигом (см. README).
		shot := a.page(t, true)
		abs, _ := filepath.Abs(dir)
		os.MkdirAll(abs, 0o755)
		for _, s := range []struct{ file, scenario, conv string }{
			{"main-top.png", "shot-top", a.main},
			{"main-bottom.png", "shot-bottom", a.main},
			{"collection.png", "shot-bottom", a.coll},
			{"empty.png", "shot-top", a.empty},
			{"window-people.png", "shot-people", a.main},
		} {
			edgeRun(t, edge, fmt.Sprintf("%s/?scenario=%s#c=%s", shot.URL, s.scenario, s.conv), "1400,900",
				"--screenshot="+filepath.Join(abs, s.file))
			t.Logf("снимок: %s", filepath.Join(abs, s.file))
		}
	}
}
