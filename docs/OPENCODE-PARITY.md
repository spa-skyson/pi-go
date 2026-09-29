# План: паритет с opencode (вариант А)

Ветка: `feature/opencode-parity`. Worktree: `.worktrees/opencode-parity`.

Цель: пользовательский сетап opencode (`~/.config/opencode/opencode.json` +
`~/.config/opencode/agent/*.md`) должен работать на pi-go. Вариант А — всё,
кроме подсистемы `permission:` (отложена в вариант Б).

## Задачи

### 1. Именованные провайдеры в config.json
**Статус:** сделано

Секция `providers` в `~/.pi-go/config.json`; имя записи — префикс модели
(`corp-claude/claude-opus-5`). Типы: `openai-compatible` (по умолчанию) и
`anthropic`. Ключ: `apiKey` (литерал, поддерживает `${VAR}` как MCP-URL) или
`apiKeyEnv`. Опционально `models` с `contextWindow` на модель. Валидация на
загрузке: коллизия со встроенным именем, пустой baseURL, кривой type — ошибка.

Проверено живым прогоном: `pi --mode print --model corp-claude/claude-opus-5`
ответил через `https://api-llm.tradedealer.xyz/v1`; лог сессии:
`provider: corp-claude`, `backend: corp-claude-custom`. `pi model list
corp-claude` листит 21 модель шлюза.

Сейчас: 10 жёстко вшитых слотов провайдеров; ключ и baseURL берутся по имени
провайдера из env (`ANTHROPIC_API_KEY`, `OPENAI_BASE_URL`...). Два своих
OpenAI-совместимых endpoint'а одновременно объявить нельзя — `--url` даёт один
безымянный `Custom`.

Надо: секция `providers` в `~/.pi-go/config.json`, где каждая запись — имя,
`baseURL`, `apiKey`/`apiKeyEnv`, `type` (`openai-compatible` | `anthropic`),
опциональный список `models`. Имя провайдера используется как префикс модели:
`corp-claude/claude-opus-5`.

Точки правки:
- `internal/config/config.go`: новый тип `ProviderConfig`, поле `Config.Providers`.
- `internal/provider/provider.go`: `Resolve`/`ResolveWithBaseURL` должны знать
  про имена из конфига (сейчас список префиксов зашит в `KnownProviderPrefixes`,
  `modelPrefixes`); `NewLLM:743` — ветка для конфигурационного провайдера;
  `ValidateModel:368` — пропускать модели такого провайдера; `APIKeyEnvVar:795`.
- `internal/config/config.go`: `autoDetectProvider:339`, `BaseURLs:754`,
  `APIKeys:726` — источником истины становится конфиг, env остаётся override.
- `internal/cli/model.go:74` (`allProviders`), `internal/provider/list_models.go`,
  `internal/ctxwindow/ctxwindow.go:34`, `internal/ratelimit/defaults.go`.

Риск: имя провайдера продублировано минимум в 7 местах. Нужен один реестр,
через который проходят все, иначе новый провайдер снова придётся вписывать
в семь switch'ей.

### 2. `model:` во frontmatter агента
**Статус:** сделано

`model:` переопределяет `role:`; имя может нести префикс named-провайдера
(`corp-codex/gpt-5.6-sol`). Named-модель → дочернему процессу не передаётся
родительский `--url` (endpoint и ключ ребёнок берёт из того же config.json);
bare-модель роли с named-провайдером получает префикс, чтобы имя было
самодостаточным.

Попутно починено два предсуществующих бага, без которых user-агенты не
работали: `SpawnWithInput` резолвил имя только по bundled (user/project-агенты
падали «unknown agent type») — теперь по registry оркестратора; и промпт,
начинающийся с имени подкоманды (`pi ping ...`), ломал спавн — перед
позиционным промптом ставится `--`. Тесты discovery изолированы от реального
HOME (задача 7 с 15 агентами их не сломает).

Проверено живым прогоном: субагент с `model: corp-claude/claude-opus-5` отвечал
через `https://api-llm.tradedealer.xyz/v1`, лог дочерней сессии:
`provider: corp-claude`, `backend: corp-claude-custom`.

Сейчас: у агента только `role:` (`internal/subagent/agents.go:127`
`applyAgentFrontmatterKey`), модель резолвится из роли
(`internal/subagent/orchestrator.go:437`).

Надо: ключ `model:` в `AgentConfig`, приоритет `model:` > `role:`.
Проброс в дочерний процесс уже есть — `SpawnOpts.Model` →
`--model` (`internal/subagent/spawner.go:113`).

Точки правки:
- `internal/subagent/agents.go`: поле `Model` в `AgentConfig:29`, ключ в
  `applyAgentFrontmatterKey:127`.
- `internal/subagent/orchestrator.go:437`: если `agent.Model != ""` — брать его,
  роль не резолвить.
- `SpawnOpts.BaseURL` — для конфигурационного провайдера нужен его baseURL,
  иначе дочерний процесс пойдёт в дефолтный endpoint.

### 3. Починка `/model`: залипание провайдера
**Статус:** сделано

Подтверждено тестом: с `provider: "anthropic"` в дефолтной роли модель
`openrouter/gemma-4` резолвится в провайдер `anthropic`.

Причина: `resolveSwitchedModel` (`internal/cli/interactive.go:1093`) безусловно
перетирает `info.Provider` значением из роли:
```go
if providerName != "" { info.Provider = providerName }
```
Тот же паттерн в `resolveRuntimeModelForRole` (`internal/cli/cli.go:380`).

Надо: не перетирать, когда модель сама несёт префикс провайдера (явное
указание сильнее дефолта роли).

Отдельно: `/model <имя>` вызывает `saveModelToConfig`
(`internal/tui/commands.go:586`, `603`) и пишет в `roles.default` и модель,
**и провайдера** — разовое переключение меняет постоянный дефолт и создаёт
залипание. Надо либо не писать провайдера, либо не персистить разовое
переключение.

### 4. Починка вывода `/model`
**Статус:** сделано

`formatModelInfo` (`internal/tui/commands.go:504`) печатает
`- %s **%s**` где первый `%s` — маркер `*` или пробел. Для активной роли
выходит `- * **default**`, и markdown-рендерер видит `- *default:` как начало
вложенного списка — роль съезжает на отдельную строку с пустым буллитом.

### 5. Оживить `tools:` у агента
**Статус:** сделано

`tools:` (запятая-список) → `SpawnOpts.Tools` → флаг `--tools` дочернего
процесса → `tools.FilterToolsByName` поверх полного набора инструментов.
Пусто/отсутствует = полный набор. Синоним `grep`/`ripgrep` матчится обоими
написаниями; неизвестные имена — warning со списком доступных. MCP-серверы при
активном `--tools` подключаются целиком по имени сервера (поштучно инструменты
за Toolset-интерфейсом недоступны; сервер, чьи инструменты всё равно не прошли
бы фильтр, не поднимается). A2A/LLMS не фильтруются.

Флаг `--tools` доступен и на самом `pi` (те же семантики), не только субагентам.

Проверено живым прогоном: агент с `tools: read` видит только read и не может
запустить bash; агент с `tools: read, bash` выполняет bash-команду.

`AgentConfig.Tools` парсится (`internal/subagent/agents.go:144`), но нигде не
применяется — мёртвое поле, тестов на применение нет.

Надо: фильтрация набора инструментов дочернего процесса. Проброса в дочерний
процесс сейчас нет вообще — `spawnArgs` (`internal/subagent/spawner.go:110`)
не передаёт список инструментов. Нужен флаг (`--tools`) или env, и фильтр
поверх `tools.CoreTools` (`internal/tools/registry.go:92`) + MCP-наборов.

### 6. `temperature`, `reasoningEffort`, `steps`
**Статус:** сделано

Frontmatter-ключи → `SpawnOpts` → флаги `--temperature`/`--thinking`/`--steps`
дочернего процесса. `LLMOptions.Temperature *float64` применяется на
OpenAI-совместимых (chat + responses) и Anthropic-путях; guard: Anthropic
отвергает temperature ≠ 1 при включённом thinking — температура не ставится,
если thinking активен. `reasoningEffort` нормализуется (minimal→none) и бьёт
конфигурационный `thinkingLevel` (флаг `--thinking` на самом `pi` — тоже).
`steps` — `agent.NewStepLimitCallback` в after-tool цепочке (до
`ComposeAfterToolChain`): (N+1)-й вызов инструмента прерывает цикл ошибкой
`steps limit N reached`. Мусорные значения во frontmatter — warning и дефолт.

Проверено живым прогоном: агент с `steps: 2` выполнил два инструментальных
вызова, третий заблокирован «steps limit 2 reached». Temperature покрыта
wire-тестами (применяется при заданном, отсутствует при nil).

Сейчас: `temperature` в коде отсутствует полностью. `reasoningEffort` частично
покрыт `thinkingLevel` (`provider.LLMOptions.ThinkingLevel`), но на уровне
агента не настраивается. `steps` (лимит итераций) — нет.

Надо: поля в `AgentConfig` и в `RoleConfig`, проброс через `SpawnOpts` во флаги
дочернего процесса, и далее в `provider.LLMOptions`.

### 7. Перенос конфигурации пользователя
**Статус:** не начато

- 15 агентов из `~/.config/opencode/agent/*.md` → `~/.pi-go/agents/`
  (механизм загрузки уже есть: `internal/subagent/agents.go:273`).
- 6 skills из `~/.config/opencode/skills/` → skills pi-go.
- MCP-серверы — уже перенесены в `~/.pi-go/config.json`.

### 8. Провайдеры `zai-coding-plan` и `opencode-go`
**Статус:** отложено пользователем (не в первую очередь)

Их ключи лежат в `~/.local/share/opencode/auth.json`, а не в `opencode.json`.
После задачи 1 они объявляются как обычные записи в `providers`.

## Порядок работ

3 → 4 → 1 → 2 → 5 → 6 → 7. Задачи 3 и 4 — независимые баг-фиксы, мелкие и
сразу проверяемые. Задача 1 — фундамент для остальных: без реестра провайдеров
`model:` у агента не сможет ссылаться на `corp-claude/...`.

## Попутная находка: тесты `internal/cli` падали от реального `~/.pi-go/.env`

Четыре теста (`TestResolvePingModelInfo`,
`TestDispatchModeRPCSetModelRejectsUnknownModels`,
`TestBuildSwitchedLLM_InvalidModel`, `TestBuildRootRuntime_InvalidModel`)
падали в полном прогоне пакета и проходили поодиночке.

Причина не в них: `loadDotEnv` (`internal/cli/cli.go:2112`) ищет
`.pi-go/.env`, поднимаясь от рабочего каталога до корня ФС — мимо
подменённого HOME, потому что этот путь к домашнему каталогу не обращается.
Из checkout'а внутри `~` он доходит до настоящего `~/.pi-go/.env` и
экспортирует его содержимое через `os.Setenv`, то есть на весь тестовый
бинарник и после конца теста-виновника.

Виновник найден бисектом по 706 тестам — `TestCliJSONModeNoPromptExitsCleanly`,
он прогоняет `runRoot`. Но точечная правка неверна: протечь может любой тест,
дошедший до `loadDotEnv`. Исправлено в `TestMain`
(`internal/cli/main_test.go`) — `os.Chdir` во временный каталог, над которым
нет `.pi-go`.

Поэтому на CI это не видно: у раннера на пути к корню нет `.pi-go`. Баг
воспроизводится только на машине разработчика с checkout'ом под `~` и
заполненным `~/.pi-go/.env`.

## Попутная находка 2: модель галлюцинирует `<<ccr:…>>` вместо длинного read

При живых проверках задач 5–6 субагент дважды «получал» вывод read как
непрозрачную ссылку `<<ccr:ed59c6732bc9,html,10.8KB>>` и отказывался читать
файл. Расследование (events.jsonl + trace-http):

- в `events.jsonl` сессии субагента functionResponse содержит **полный текст**
  файла;
- в `http_request` к провайдеру — тоже полный текст, `ccr` в теле отсутствует;
- строки `ccr` нет нигде в коде pi-go и adk;
- «хэш» каждый раз новый, тип `html` для go.mod абсурден, частота ~50%
  (2 из 4 прогонов; с трейсом и контрольные прогоны — чистые).

Вывод: это нестабильная галлюцинация claude-opus-5 за корпоративным шлюзом
(`corp-claude`), конструирующая правдоподобный формат из обучающих данных.
Подмены на пути pi-go нет. Митигация: одна строка в системной инструкции
агента — результаты инструментов приходят как есть, формата
плейсхолдеров-ссылок не существует.

## Проверка

Каждый шаг: `make test-unit`, `make vet`, `make lint`. Итоговая проверка —
реальный запрос через каждый провайдер и подтверждение по логу сессии
(`~/.pi-go/log/<дата>/session-*.log`, поля `provider` и `backend`), а не по
факту успешного ответа.
