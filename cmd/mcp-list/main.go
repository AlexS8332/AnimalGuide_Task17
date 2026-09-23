// Команда mcp-list — минимальный MCP-клиент.
//
// Делает ровно то, с чего начинается любая работа с MCP: устанавливает
// соединение с сервером, выполняет handshake (initialize) и запрашивает
// список инструментов (tools/list). Всё, что сервер рассказал о себе и
// своих инструментах, печатается человеку.
//
// Сервер запускается дочерним процессом, общение идёт по его stdin и
// stdout. По умолчанию запускается сервер из этого репозитория, но в
// аргументах можно передать любую другую команду: клиент ничего не знает
// про животных, он знает только протокол.
//
//	mcp-list                                 # сервер из этого репозитория
//	mcp-list -schemas                        # плюс полные JSON Schema
//	mcp-list -i                              # ручной режим: команды с клавиатуры
//	mcp-list -call read_wikipedia -args 'title=Манул section=Питание'
//	mcp-list -- npx @modelcontextprotocol/server-everything
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// defaultServer — сервер из этого репозитория. `go run` вместо готового
// бинарника, чтобы клиент работал сразу после клонирования.
var defaultServer = []string{"go", "run", "./cmd/animals-mcp"}

// serverPackage — каталог пакета сервера относительно корня репозитория.
const serverPackage = "cmd/animals-mcp"

// repoRoot ищет корень репозитория вверх от рабочего каталога.
//
// Рабочий каталог клиенту никто не гарантирует: отладчик VS Code
// запускает программу из каталога её пакета, и относительный путь
// `./cmd/animals-mcp` оттуда никуда не ведёт. Поэтому корень находим
// сами и запускаем сервер из него.
func repoRoot() (string, bool) {
	dir, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(serverPackage))); err == nil {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

// demoCalls — проверочные вызовы после получения списка. Список
// инструментов сам по себе ещё не доказывает, что сервер работает:
// доказывает вызов. Нужна сеть: сервер ходит в Википедию и GBIF.
var demoCalls = []struct {
	Tool string
	Args map[string]any
}{
	{Tool: "search_wikipedia", Args: map[string]any{"query": "рысь"}},
	{Tool: "match_taxon", Args: map[string]any{"scientific_name": "Lynx lynx"}},
	// Ошибка инструмента — тоже результат: сервер отвечает IsError с
	// текстом, а не сбоем протокола.
	{Tool: "read_wikipedia", Args: map[string]any{}},
	// Служебный инструмент последним: в счётчиках видны вызовы выше.
	{Tool: "server_info"},
}

func main() {
	enableUTF8Console()

	var (
		schemas  = flag.Bool("schemas", false, "печатать полные JSON Schema аргументов")
		call     = flag.String("call", "", "вызвать только этот инструмент")
		args     = flag.String("args", "", "аргументы для -call: JSON-объект или пары ключ=значение")
		noDemo   = flag.Bool("no-demo", false, "не делать проверочные вызовы, только список инструментов")
		interact = flag.Bool("i", false, "ручной режим: вводить команды и вызовы с клавиатуры")
		full     = flag.Bool("full", false, "печатать результаты вызовов целиком, без обрезки")
		timeout  = flag.Duration("timeout", 2*time.Minute, "предел времени: на всю работу, а в ручном режиме — на каждый вызов")
	)
	flag.Usage = usage
	flag.Parse()

	// Свою команду запускаем как есть, в текущем каталоге. Для сервера
	// из этого репозитория каталог выбираем сами: относительный путь к
	// его пакету имеет смысл только из корня.
	command, dir := flag.Args(), ""
	if len(command) == 0 {
		root, ok := repoRoot()
		if !ok {
			fmt.Fprintf(os.Stderr, "ошибка: не нашёл каталог %s ни в рабочем каталоге, ни выше по дереву.\n", serverPackage)
			fmt.Fprintln(os.Stderr, "Запустите клиента из репозитория или передайте команду сервера аргументом:")
			fmt.Fprintln(os.Stderr, "  mcp-list -- путь/к/animals-mcp")
			os.Exit(1)
		}
		command, dir = defaultServer, root
	}

	// Ctrl+C прерывает и долгий вызов, и ожидание ввода.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Общий предел времени — только для пакетных запусков. В ручном
	// режиме человек думает между командами сколько хочет, а таймаут
	// отсчитывается на каждый вызов отдельно.
	if !*interact {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}

	if err := run(ctx, command, dir, options{
		schemas:     *schemas,
		call:        *call,
		args:        *args,
		noDemo:      *noDemo,
		full:        *full,
		interactive: *interact,
		timeout:     *timeout,
	}); err != nil {
		fmt.Fprintln(os.Stderr, "ошибка:", err)
		os.Exit(1)
	}
}

type options struct {
	schemas     bool
	call        string
	args        string
	noDemo      bool
	full        bool
	interactive bool
	timeout     time.Duration
}

func run(ctx context.Context, command []string, dir string, o options) error {
	// 1. Соединение. Клиент запускает сервер и говорит с ним по stdio;
	// stderr сервера остаётся нашим, поэтому его сообщения видно.
	cmd := exec.Command(command[0], command[1:]...)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "mcp-list",
		Version: "1.0.0",
		Title:   "Минимальный MCP-клиент",
	}, nil)

	section("Соединение")
	field("транспорт", "stdio, сервер запущен дочерним процессом")
	field("команда", strings.Join(command, " "))
	// Каталог называем, только когда он не совпадает с текущим: это
	// диагностика для запуска из отладчика или из подкаталога, а при
	// обычном запуске из корня строка была бы шумом.
	if cwd, err := os.Getwd(); err == nil && dir != "" && dir != cwd {
		field("каталог", dir)
	}

	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		return fmt.Errorf("соединение не установлено: %w", err)
	}
	defer session.Close()

	// 2. Что сервер рассказал о себе при рукопожатии.
	init := session.InitializeResult()
	fmt.Println()
	section("Сервер")
	field("имя", fmt.Sprintf("%s %s", init.ServerInfo.Name, init.ServerInfo.Version))
	if init.ServerInfo.Title != "" {
		field("название", init.ServerInfo.Title)
	}
	field("протокол", init.ProtocolVersion)
	field("умеет", capabilities(init.Capabilities))
	if init.Instructions != "" {
		field("инструкция", init.Instructions)
	}

	// 3. Список инструментов. Ради этого всё и затевалось.
	if init.Capabilities.Tools == nil {
		fmt.Println("\nСервер не объявил возможность tools: списка инструментов не будет.")
		return nil
	}

	tools, err := listTools(ctx, session)
	if err != nil {
		return err
	}
	fmt.Println()
	section(fmt.Sprintf("Инструменты (%d)", len(tools)))
	for i, t := range tools {
		printTool(i+1, t, o.schemas)
	}

	// 4. Вызовы: список — это обещание сервера, вызов — его исполнение.
	if o.call != "" {
		fmt.Println()
		section("Вызов")
		arguments, err := parseArgs(o.args)
		if err != nil {
			return fmt.Errorf("-args: %w", err)
		}
		if err := callTool(ctx, session, o.call, arguments, o.full); err != nil {
			return err
		}
		if !o.interactive {
			return nil
		}
	}

	// 5. Ручной режим: дальше команды набирает человек.
	if o.interactive {
		return repl(ctx, session, tools, o.full, o.timeout)
	}
	if o.noDemo {
		return nil
	}

	fmt.Println()
	section("Проверочные вызовы")
	for _, c := range demoCalls {
		if err := callTool(ctx, session, c.Tool, c.Args, o.full); err != nil {
			return err
		}
	}
	return nil
}

// listTools забирает весь список, сколько бы страниц в нём ни было.
// Итератор Tools сам ходит за следующими страницами по курсору.
func listTools(ctx context.Context, session *mcp.ClientSession) ([]*mcp.Tool, error) {
	var tools []*mcp.Tool
	for t, err := range session.Tools(ctx, nil) {
		if err != nil {
			return nil, fmt.Errorf("список инструментов не получен: %w", err)
		}
		tools = append(tools, t)
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools, nil
}

func printTool(n int, t *mcp.Tool, withSchema bool) {
	title := t.Name
	if t.Title != "" {
		title += " — " + t.Title
	}
	fmt.Printf("\n%2d. %s\n", n, title)
	for _, line := range wrap(t.Description, 76) {
		fmt.Printf("    %s\n", line)
	}

	raw, err := json.Marshal(t.InputSchema)
	if err != nil {
		fmt.Printf("    схема аргументов не читается: %v\n", err)
		return
	}
	props, err := schemaProps(raw)
	if err != nil {
		fmt.Printf("    схема аргументов не читается: %v\n", err)
		return
	}

	if len(props) == 0 {
		fmt.Println("    аргументов нет")
	} else {
		fmt.Println("    аргументы:")
		// Обязательные аргументы — первыми: схема перечисляет свойства
		// по алфавиту, а читателю важнее знать, без чего вызов не пройдёт.
		sort.SliceStable(props, func(i, j int) bool {
			if props[i].Required != props[j].Required {
				return props[i].Required
			}
			return props[i].Name < props[j].Name
		})
		width, typeWidth := 0, 0
		for _, p := range props {
			width = max(width, len(p.Name))
			typeWidth = max(typeWidth, len(p.Type))
		}
		for _, p := range props {
			mark := " "
			if p.Required {
				mark = "*"
			}
			head := fmt.Sprintf("      %s %-*s  %-*s ", mark, width, p.Name, typeWidth, p.Type)
			description := p.Description
			if len(p.Enum) > 0 {
				description += " (" + strings.Join(p.Enum, ", ") + ")"
			}
			printWrapped(head, description)
		}
		fmt.Println("      * — обязательный аргумент")
	}

	if withSchema {
		fmt.Println("    схема:")
		for _, line := range strings.Split(indentJSON(string(raw)), "\n") {
			fmt.Printf("      %s\n", line)
		}
	}
}

func callTool(ctx context.Context, session *mcp.ClientSession, name string, arguments map[string]any, full bool) error {
	shown, _ := json.Marshal(arguments)
	if arguments == nil {
		shown = []byte("{}")
	}
	fmt.Printf("\n  → %s %s\n", name, shown)

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		// Сюда попадают сбои протокола: неизвестный инструмент,
		// невалидные аргументы, оборванное соединение.
		return fmt.Errorf("вызов %s не прошёл: %w", name, err)
	}

	for _, c := range res.Content {
		text, ok := c.(*mcp.TextContent)
		if !ok {
			fmt.Printf("    [%T]\n", c)
			continue
		}
		body := indentJSON(text.Text)
		if !full {
			body = clip(body, 16, 900)
		}
		for _, line := range strings.Split(body, "\n") {
			fmt.Printf("    %s\n", line)
		}
	}
	// Ошибка самого инструмента (не протокола) приезжает обычным
	// результатом с поднятым флагом: сервер жив, а вот вызов — нет.
	if res.IsError {
		fmt.Println("    (инструмент сообщил об ошибке)")
	}
	return nil
}

func capabilities(c *mcp.ServerCapabilities) string {
	if c == nil {
		return "ничего не объявил"
	}
	var out []string
	if c.Tools != nil {
		s := "tools"
		if c.Tools.ListChanged {
			s += " (сообщает об изменениях списка)"
		}
		out = append(out, s)
	}
	if c.Resources != nil {
		out = append(out, "resources")
	}
	if c.Prompts != nil {
		out = append(out, "prompts")
	}
	if c.Completions != nil {
		out = append(out, "completions")
	}
	if len(out) == 0 {
		return "ничего не объявил"
	}
	return strings.Join(out, ", ")
}

func section(title string) {
	fmt.Println(title)
	fmt.Println(strings.Repeat("─", len([]rune(title))))
}

func field(name, value string) {
	name += ":"
	head := "  " + name + strings.Repeat(" ", max(1, 13-runes(name)))
	printWrapped(head, value)
}

// printWrapped печатает текст после заголовка, перенося продолжение под
// ту же колонку. Отступ считается в рунах, а не в байтах: кириллическая
// буква занимает в UTF-8 два байта, и выравнивание по len() разъезжается.
func printWrapped(head, text string) {
	width := runes(head)
	lines := wrap(text, 80-width)
	if len(lines) == 0 {
		fmt.Println(strings.TrimRight(head, " "))
		return
	}
	fmt.Println(head + lines[0])
	for _, line := range lines[1:] {
		fmt.Printf("%s%s\n", strings.Repeat(" ", width), line)
	}
}

func runes(s string) int { return len([]rune(s)) }

// wrap переносит текст по словам: описания инструментов длинные, а
// терминал узкий.
func wrap(s string, width int) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if width < 20 {
		width = 20
	}

	var (
		lines []string
		line  []rune
	)
	for _, word := range strings.Fields(s) {
		w := []rune(word)
		switch {
		case len(line) == 0:
			line = w
		case len(line)+1+len(w) <= width:
			line = append(append(line, ' '), w...)
		default:
			lines = append(lines, string(line))
			line = w
		}
	}
	return append(lines, string(line))
}

func usage() {
	fmt.Fprintln(os.Stderr, "mcp-list — подключается к MCP-серверу и печатает список его инструментов.")
	fmt.Fprintln(os.Stderr, "\nИспользование:\n  mcp-list [флаги] [-- команда запуска сервера]")
	fmt.Fprintln(os.Stderr, "\nБез команды запускается сервер из этого репозитория:")
	fmt.Fprintln(os.Stderr, "  "+strings.Join(defaultServer, " "))
	fmt.Fprintln(os.Stderr, "\nФлаги:")
	flag.PrintDefaults()
}
