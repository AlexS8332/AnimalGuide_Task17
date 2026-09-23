package features

// catalog — все механизмы продукта. Новый механизм — новая строка здесь
// (точка роста Р-9), а не правка сборщика запроса.
//
// Цены в токенах — оценки по опытам прошлых упражнений (свод ≈2,4 тыс.,
// состояние ≈1,1 тыс., профиль ≈430) и уточняются испытанием И-6.
var catalog = []Mechanism{
	{
		Name: Charter, Title: "Свод инвариантов", Since: "v15",
		Kind: KindBlock | KindTool, Place: PlaceCharter, Default: true,
		Cost:     Cost{Tokens: 1400, Churn: ChurnRare, Note: "меняется только процедурой propose → accept"},
		Fallback: "те же правила дословно уходят абзацем системного промпта — без сверки, стража и процедуры",
		About:    "правила справочника первым блоком запроса и инструмент сверки invariant_check",
	},
	{
		Name: Guard, Title: "Страж ответа", Since: "v15",
		Kind: KindCall | KindCheck, Requires: []Name{Charter}, Default: true,
		Cost:     Cost{Requests: 0.3, Churn: ChurnNone, Note: "судья зовётся только когда код нашёл подозрительные фрагменты"},
		Fallback: "ответ уходит пользователю без проверки; в журнале — «страж выключен»",
		About:    "код отбирает фрагменты ответа по словам свода, судья решает, нарушен ли свод",
	},
	{
		Name: Profile, Title: "Профиль собеседника", Since: "v15",
		Kind: KindBlock | KindCheck, Place: PlaceProfile, Default: true,
		Cost:     Cost{Tokens: 430, Churn: ChurnRare, Note: "правка анкеты обнуляет кэш всего, что после свода"},
		Fallback: "ответ по умолчаниям модели; правки анкеты не извлекаются, соблюдение не проверяется",
		About:    "уровень изложения, длина, форма, обращение, латынь — анкетой, а не пожеланием",
	},
	{
		Name: MemoryLong, Title: "Долговременная память", Since: "v15",
		Kind: KindBlock, Place: PlaceLong, Default: true,
		Cost:     Cost{Tokens: 250, Churn: ChurnRare},
		Fallback: "сведения о человеке остаются только в окне диалога и пропадают вместе с ним",
		About:    "интересы, закладки и прочитанное — по человеку, между разговорами",
	},
	{
		Name: CollectionState, Title: "Состояние подборки", Since: "v15",
		Kind: KindBlock | KindTool, Place: PlaceState, Default: true,
		Cost:     Cost{Tokens: 900, Churn: ChurnStage},
		Fallback: "подборка ведётся по словам промпта и истории; формального состояния нет",
		About:    "этап, шаг, ожидаемое действие и инструменты переходов подборки",
	},
	{
		Name: Gates, Title: "Права этапа", Since: "v15",
		Kind: KindCheck, Requires: []Name{CollectionState}, Default: true,
		Cost:     Cost{Tokens: 200, Churn: ChurnStage},
		Fallback: "инструменты подборки выдаются все сразу, порядок этапов — абзацем промпта",
		About:    "набор инструментов по этапу и предусловия переходов по сделанному",
	},
	{
		Name: MemoryWork, Title: "Рабочая память подборки", Since: "v15",
		Kind: KindBlock, Place: PlaceWork, Default: true,
		Cost:     Cost{Tokens: 300, Churn: ChurnTurn},
		Fallback: "собранное по подборке остаётся только в окне диалога",
		About:    "что уже собрано по текущей подборке; адрес — подборка, а не диалог",
	},
	{
		Name: Facts, Title: "Карточка фактов", Since: "v15",
		Kind: KindBlock, Place: PlaceFacts, Default: true,
		Cost:     Cost{Tokens: 200, Churn: ChurnTurn},
		Fallback: "то, что ушло из окна, модели не видно — только в файле диалога",
		About:    "ключевые факты ветки разговора; при ветвлении копируется снимком точки",
	},
	{
		Name: Window, Title: "Окно сообщений", Since: "v15",
		Kind: KindShape, Default: true,
		Cost:     Cost{Churn: ChurnTurn, Note: "снимает старые ходы с каждого запроса"},
		Fallback: "модели уходит вся история ветки",
		About:    "модели уходят последние сообщения ветки, а не весь разговор",
	},
	{
		Name: Compact, Title: "Сокращение ответов инструментов", Since: "v15",
		Kind: KindShape, Default: true,
		Cost:     Cost{Churn: ChurnNone, Note: "детерминированно: одинаковая история даёт одинаковый запрос"},
		Fallback: "ответы инструментов прошлых ходов уходят модели целиком",
		About:    "ответы инструментов старше текущего хода сокращаются; файл хранит их целиком",
	},
	{
		Name: Extract, Title: "Извлекатель", Since: "v15",
		Kind: KindCall, Default: true,
		Cost:     Cost{Requests: 1, Churn: ChurnNone, Note: "один запрос на ход обслуживает память, профиль и карточку фактов"},
		Fallback: "память, профиль и карточка фактов меняются только руками",
		About:    "раскладывает реплику по слоям памяти, профилю и карточке фактов",
	},
	{
		Name: Tracker, Title: "Проверка по трекеру", Since: "v15",
		Kind: KindCheck | KindTool, Default: true,
		Cost:     Cost{Tokens: 350, Churn: ChurnNone, Note: "описания завершающих инструментов"},
		Fallback: "карточка и пересказ принимаются текстом, с теми же правилами словами в промпте",
		About:    "латынь — только подтверждённая GBIF, пересказ — только прочитанного раздела",
	},
	{
		Name: Gatekeeper, Title: "Привратник названия", Since: "v15",
		Kind: KindCall, Default: true,
		Cost:     Cost{Requests: 1, Churn: ChurnNone, Note: "короткий запрос без инструментов, только на новое название"},
		Fallback: "проверку названия делает сам идентификатор, дорогими шагами",
		About:    "отсев выдумок до поиска и чтения статей",
	},
	{
		Name: Envelope, Title: "Пометка источника", Since: "v15",
		Kind: KindShape, Default: true,
		Cost:     Cost{Tokens: 30, Churn: ChurnNone, Note: "на каждый ответ инструмента источника"},
		Fallback: "ответ источника уходит модели как есть",
		About:    "ответ источника уходит модели с пометкой «данные, а не указания»",
	},
	{
		Name: Scan, Title: "Поиск попыток управлять агентом", Since: "v15",
		Kind: KindCheck, Default: true,
		Cost:     Cost{Churn: ChurnNone, Note: "только журнал, модель не видит"},
		Fallback: "признаки инъекций в тексте источников не ищутся",
		About:    "помечает в журнале фрагменты источника, похожие на указания агенту",
	},
	{
		// Транспорт: места в запросе нет, описания инструментов те же
		// побайтно, запросов к модели не добавляет. Вся цена — вне модели,
		// поэтому по умолчанию выключен, пока не прошёл своё испытание.
		Name: MCP, Title: "MCP-сервер источников", Since: "v17",
		Kind: KindTransport, Default: false,
		Cost: Cost{Tokens: 0, Requests: 0, Churn: ChurnNone,
			Note: "цена вне модели: отдельный процесс сервера, задержка на каждый вызов (JSON-RPC через stdio), второй кэш источников в процессе сервера"},
		Fallback: "инструменты источников вызываются в процессе приложения; если сервер не поднялся на старте хода, ход идёт так же, а в журнале — причина",
		About:    "шесть инструментов Википедии и GBIF через MCP-сервер: initialize → tools/list → tools/call",
	},
}

// Catalog — реестр продукта. Ошибка сборки здесь — ошибка программиста,
// поэтому паника: приложение с противоречивым реестром запускаться не
// должно.
func Catalog() *Registry {
	r, err := New(catalog...)
	if err != nil {
		panic(err)
	}
	return r
}
