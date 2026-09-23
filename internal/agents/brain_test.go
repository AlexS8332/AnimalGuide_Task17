package agents

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/AlexS8332/AnimalGuide/internal/llm"
	"github.com/AlexS8332/AnimalGuide/internal/llm/llmtest"
	"github.com/AlexS8332/AnimalGuide/internal/tools"
)

// brain — подставная модель, которая ведёт себя как добросовестный агент:
// отвечает по тому, кто спрашивает (системный промпт) и что уже вернули
// инструменты. Отклонения от добросовестности включаются полями.
type brain struct {
	mu sync.Mutex
	// GateNo — названия, на которые привратник отвечает НЕТ.
	GateNo []string
	// FakeLatin — идентификатор сначала сдаёт эту латынь, не сверив её.
	FakeLatin string
	// LeadScript — ответы ведущего по шагам (сколько ответов инструментов
	// уже в запросе).
	LeadScript func(req llm.Request, step int) llm.Response
	// TextOnly — идентификатор отвечает текстом вместо инструмента.
	TextOnly bool
	calls    map[string]int
}

func (b *brain) count(who string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.calls == nil {
		b.calls = map[string]int{}
	}
	b.calls[who]++
}

func (b *brain) Calls(who string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls[who]
}

func lastUser(req llm.Request) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == llm.RoleUser {
			return req.Messages[i].Content
		}
	}
	return ""
}

// toolReplies — ответы инструментов этого прогона с их именами, без обёртки.
func toolReplies(req llm.Request) []struct{ Name, Out string } {
	names := map[string]string{}
	var out []struct{ Name, Out string }
	for _, m := range req.Messages {
		for _, c := range m.ToolCalls {
			names[c.ID] = c.Function.Name
		}
		if m.Role == llm.RoleTool {
			data, _ := tools.Unwrap(m.Content)
			out = append(out, struct{ Name, Out string }{names[m.ToolCallID], data})
		}
	}
	return out
}

func lastReply(req llm.Request, name string) string {
	rs := toolReplies(req)
	for i := len(rs) - 1; i >= 0; i-- {
		if rs[i].Name == name {
			return rs[i].Out
		}
	}
	return ""
}

var latinRe = regexp.MustCompile(`лат\. ([A-Z][a-z]+ [a-z]+)`)
var quotedRe = regexp.MustCompile(`«([^»]+)»`)

func args(v any) string {
	data, _ := json.Marshal(v)
	return string(data)
}

func (b *brain) Chat(req llm.Request) (llm.Response, error) {
	sys := req.Messages[0].Content
	switch {
	case strings.HasPrefix(sys, "Ты — зоолог-систематик"):
		b.count("gatekeeper")
		name := strings.ToLower(lastUser(req))
		for _, no := range b.GateNo {
			if strings.Contains(name, no) {
				return llmtest.Text("- | НЕТ"), nil
			}
		}
		return llmtest.Text("Lynx lynx | ДА"), nil
	case strings.Contains(sys, "агент-идентификатор"):
		b.count("identifier")
		return b.identifier(req), nil
	case strings.Contains(sys, "агент-специалист по разделу"):
		b.count("section")
		return b.section(req), nil
	case strings.Contains(sys, "агент сравнения"):
		b.count("comparer")
		return b.comparer(req), nil
	case strings.Contains(sys, "ведёшь разговор справочника"):
		b.count("lead")
		if b.LeadScript != nil {
			return b.LeadScript(req, len(toolReplies(req))), nil
		}
		return llmtest.Text("Ответ ведущего."), nil
	}
	return llm.Response{}, fmt.Errorf("неизвестный агент: %.60s", sys)
}

func (b *brain) identifier(req llm.Request) llm.Response {
	query := quotedRe.FindStringSubmatch(lastUser(req))
	q := ""
	if query != nil {
		q = query[1]
	}
	if b.TextOnly {
		return llmtest.Text(`{"name_ru":"Рысь","latin":"Lynx rufus","wiki_title":"Рысь","summary":"по памяти"}`)
	}
	search := lastReply(req, "search_wikipedia")
	if search == "" {
		return llmtest.ToolCall("search_wikipedia", args(map[string]string{"query": q}))
	}
	var hits struct {
		Results []struct{ Title string } `json:"results"`
	}
	json.Unmarshal([]byte(search), &hits)
	title := ""
	for _, h := range hits.Results {
		// Добросовестно: берём статью о том же животном, похожее — нет.
		if strings.Contains(strings.ToLower(h.Title), strings.ToLower(lastWord(q))) && !strings.Contains(strings.ToLower(q), "полосат") {
			title = h.Title
		}
	}
	if title == "" {
		return llmtest.ToolCall("report_not_found", `{"reason":"статьи именно об этом животном нет"}`)
	}
	read := lastReply(req, "read_wikipedia")
	if read == "" {
		return llmtest.ToolCall("read_wikipedia", args(map[string]string{"title": title}))
	}
	var art struct {
		Title string `json:"title"`
		Intro string `json:"intro"`
	}
	json.Unmarshal([]byte(read), &art)
	m := latinRe.FindStringSubmatch(art.Intro)
	if m == nil {
		return llmtest.ToolCall("report_not_found", `{"reason":"латынь не найдена"}`)
	}
	latin := m[1]
	if b.FakeLatin != "" && !refused(req) {
		return llmtest.ToolCall("submit_card", args(map[string]any{"name_ru": q, "latin": b.FakeLatin, "wiki_title": art.Title, "summary": "по памяти"}))
	}
	if lastReply(req, "match_taxon") == "" {
		return llmtest.ToolCall("match_taxon", args(map[string]string{"scientific_name": latin}))
	}
	return llmtest.ToolCall("submit_card", args(map[string]any{
		"name_ru": art.Title, "latin": latin, "wiki_title": art.Title, "summary": firstSentence(art.Intro),
		"tree_ru": []map[string]string{{"name": "Felidae", "name_ru": "Кошачьи"}, {"name": "Mammalia", "name_ru": "Млекопитающие"}},
	}))
}

// refused — получала ли модель в этом прогоне отказ завершающего
// инструмента: после отказа добросовестная модель делает, что велено.
func refused(req llm.Request) bool {
	for _, m := range req.Messages {
		if m.Role == llm.RoleTool && strings.Contains(m.Content, "Не принято") {
			return true
		}
	}
	return false
}

func lastWord(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return s
	}
	w := f[len(f)-1]
	if r := []rune(w); len(r) > 4 {
		return string(r[:len(r)-1])
	}
	return w
}

func firstSentence(s string) string {
	if i := strings.Index(s, "."); i > 0 {
		return s[:i+1]
	}
	return s
}

func (b *brain) section(req llm.Request) llm.Response {
	user := lastUser(req)
	title := quotedRe.FindStringSubmatch(user)[1]
	topic := ""
	if i := strings.LastIndex(user, "Тема: "); i >= 0 {
		topic = strings.TrimSuffix(strings.TrimSpace(user[i+len("Тема: "):]), ".")
	}
	// Кандидаты по теме, как в промпте.
	want := map[string]string{"Питание": "питание", "Ареал": "распространение", "Статус охраны": "охран",
		"Размножение": "размножение", "Образ жизни": "образ жизни"}[topic]
	read := lastReply(req, "read_wikipedia")
	if read == "" {
		return llmtest.ToolCall("read_wikipedia", args(map[string]string{"title": title, "section": want}))
	}
	var sec struct {
		Found   bool   `json:"found"`
		Section string `json:"section"`
		Text    string `json:"text"`
	}
	json.Unmarshal([]byte(read), &sec)
	if !sec.Found {
		return llmtest.ToolCall("submit_section", args(map[string]any{"found": false, "section": "", "text": "в статье нет раздела о теме «" + topic + "»"}))
	}
	return llmtest.ToolCall("submit_section", args(map[string]any{"found": true, "section": sec.Section, "text": "Пересказ: " + sec.Text}))
}

func (b *brain) comparer(req llm.Request) llm.Response {
	titles := quotedRe.FindAllStringSubmatch(lastUser(req), -1)
	a, c := titles[0][1], titles[1][1]
	reads := 0
	for _, r := range toolReplies(req) {
		if r.Name == "read_wikipedia" {
			reads++
		}
	}
	switch reads {
	case 0:
		return llmtest.ToolCalls(
			llmtest.Call{Name: "read_wikipedia", Args: args(map[string]string{"title": a, "section": "Распространение"})},
			llmtest.Call{Name: "read_wikipedia", Args: args(map[string]string{"title": c, "section": "Распространение"})})
	}
	return llmtest.ToolCall("submit_comparison", args(map[string]any{"rows": []map[string]string{
		{"aspect": "Ареал", "a": "леса Евразии", "a_section": "Распространение", "b": "степи Азии", "b_section": "Распространение"},
		{"aspect": "Размеры", "a": "крупная", "a_section": "Внешний вид", "b": "сведений нет", "b_section": ""},
	}}))
}
