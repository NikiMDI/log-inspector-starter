# «Кто положил сервис?»

Сервис принимает access-лог Nginx в JSON Lines, выполняет анализ фоновыми worker и сохраняет задания и отчёты в PostgreSQL.

```text
клиент -> Nginx -> Go API -> PgBouncer (transaction) -> PostgreSQL
                         \-> Docker volume с загрузками
```

В основной конфигурации работает одна реплика Go. Worker атомарно забирают задания SQL-запросом с `FOR UPDATE SKIP LOCKED`, поэтому внутри процесса задание выполняется ровно одним worker. Исходный файл читается последовательно; в памяти остаются только карты маршрутов, IP и минут.

## Быстрый запуск

Нужны Docker Compose и свободные порты 8080 и 6060.

Значения `loginspector` в Compose — только локальные демонстрационные настройки. Для своего запуска скопируйте `.env.example` в `env/secrets.local`, замените пароль и добавляйте `--env-file env/secrets.local` после файла окружения; `*.local` исключены из Git.

```powershell
docker compose --env-file env/dev.env up --build -d
curl.exe http://localhost:8080/health
```

`migrate` подключается прямо к PostgreSQL и должен успешно завершиться до запуска приложения. Приложение подключается только через PgBouncer. Для остановки без удаления данных:

```powershell
docker compose --env-file env/dev.env down
```

Тома удаляются только намеренно командой `down -v`.

## API

Загрузка передаётся непосредственно в теле запроса. Необязательный заголовок `X-Filename` сохраняет исходное имя файла.

```powershell
$job = curl.exe -s -X POST http://localhost:8080/jobs `
  -H "Content-Type: application/x-ndjson" `
  -H "X-Filename: access.jsonl" `
  --data-binary "@fixtures/access.jsonl" | ConvertFrom-Json

curl.exe "http://localhost:8080/jobs/$($job.id)"
curl.exe "http://localhost:8080/jobs/$($job.id)/report"
```

Маршруты API:

- `POST /jobs` — `202 {"id":"..."}`;
- `GET /jobs/{id}` — состояние `queued`, `running`, `done` или `failed` и поле `error` при ошибке;
- `GET /jobs/{id}/report` — отчёт или 409 до состояния `done`;
- `GET /health` — работоспособность HTTP-приложения.

Все ошибки имеют вид `{"error":"текст"}`. Неизвестный ID даёт 404, неверный Content-Type — 415, превышение ограничения загрузки — 413. Лимиты задаются `MAX_UPLOAD_BYTES` и `MAX_LINE_BYTES`. Ошибка входной строки переводит задание в `failed`, а её номер попадает в `error`; частичный отчёт не сохраняется.

## Проверки

Для локальных Go-команд требуется Go 1.26+:

```powershell
go test ./...
go test -race ./...
docker compose run --rm test-runner
```

Тест анализатора сравнивает контрольный файл с `fixtures/expected.json`, проверяет ошибку на второй строке `fixtures/invalid.jsonl` и пустой вход. Генератор больших файлов:

```powershell
New-Item -ItemType Directory -Force generated | Out-Null
go run ./cmd/loggen -rows 100000 -seed 42 -scenario slow > generated/large.jsonl
```

### Один и два worker

В `dev` установлен один worker и искусственная задержка 1 секунда. Отправьте несколько заданий подряд и смотрите переходы и логи:

```powershell
1..4 | ForEach-Object { curl.exe -s -X POST http://localhost:8080/jobs -H "Content-Type: application/x-ndjson" --data-binary "@fixtures/access.jsonl" }
docker compose --env-file env/dev.env logs -f app
```

Затем сравните с двумя worker без задержки:

```powershell
$env:PROCESSING_DELAY = "2s"
docker compose --env-file env/test.env up --build -d
Remove-Item Env:PROCESSING_DELAY
docker compose --env-file env/test.env logs -f app
```

Имена Compose-проектов и порты различаются, поэтому dev и test используют разные сети и тома.

### Перезапуск и сохранность

После получения `done` перезапустите приложение и повторно запросите тот же отчёт:

```powershell
docker compose --env-file env/dev.env restart app
curl.exe "http://localhost:8080/jobs/$($job.id)/report"
```

При старте оставшиеся `queued` и `running` переводятся в `failed` с текстом `обработка прервана перезапуском`. Готовые отчёты не меняются.

### Nginx и PgBouncer

Реальный JSON access-лог после любого запроса:

```powershell
docker compose --env-file env/dev.env exec nginx tail -n 5 /var/log/nginx/access.jsonl
```

Состояние пула (административная БД PgBouncer):

```powershell
docker compose --env-file env/dev.env exec -e PGPASSWORD=loginspector pgbouncer `
  psql -h 127.0.0.1 -p 6432 -U loginspector pgbouncer -c "SHOW POOLS;"
```

`sql.DB` — клиентский пул внутри Go-процесса: ограничивает конкурентные подключения приложения и показывает ожидание через `WaitCount`/`WaitDuration`. PgBouncer — отдельный сетевой пул между приложением и PostgreSQL: в режиме `transaction` серверное соединение закрепляется только на время транзакции и может обслуживать много клиентских соединений. Приложение использует `lib/pq`, поэтому кеш именованных prepared statements с transaction pooling не возникает.

Статистика маленького Go-пула в test (`DB_MAX_OPEN=1`) доступна только на локальном диагностическом порту:

```powershell
curl.exe http://localhost:6061/debug/db-stats
```

После нескольких параллельных запросов рост `WaitCount` означает, что goroutine ждали свободное соединение. Это ожидание допустимо и ограничивает давление на БД.

### pprof памяти

Создайте большой файл, загрузите его, затем во время обработки снимите профиль:

```powershell
New-Item -ItemType Directory -Force output | Out-Null
$job = curl.exe -s -X POST http://localhost:8080/jobs `
  -H "Content-Type: application/x-ndjson" `
  --data-binary "@generated/large.jsonl" | ConvertFrom-Json
curl.exe -o output/heap.prof http://localhost:6060/debug/pprof/heap
curl.exe -o output/allocs.prof http://localhost:6060/debug/pprof/allocs
go tool pprof -top output/heap.prof
go tool pprof -top output/allocs.prof
```

Размер файла подбирается под ноутбук. В нормальном сценарии память определяется картами агрегатов (`routes`, `ips`, `minutes`), JSON-буфером одной строки и служебными объектами runtime, а не числом исходных строк. В комплектном генераторе набор ключей ограничен, поэтому увеличение числа строк в основном увеличивает время работы и размер файла, но не карты.

## Миграции с сохранением данных

Миграции встроены в отдельный бинарник и применяются по имени один раз в транзакции. `001_create_jobs.sql` создаёт таблицу, `002_add_original_filename.sql` содержательно добавляет используемое API поле.

Для демонстрации обновления отдельно от обычного запуска:

```powershell
docker compose -p log-inspector-migration-check up -d postgres
docker compose -p log-inspector-migration-check run --rm -e MIGRATION_TARGET=001_create_jobs.sql migrate
docker compose -p log-inspector-migration-check exec postgres psql -U loginspector -d loginspector -c "INSERT INTO jobs(id,status,file_path,report) VALUES ('before-v2','done','/tmp/demo','{}');"
docker compose -p log-inspector-migration-check run --rm migrate
docker compose -p log-inspector-migration-check exec postgres psql -U loginspector -d loginspector -c "SELECT id,status,original_filename FROM jobs WHERE id='before-v2';"
```

Последний запрос показывает старую запись и новое поле с пустым значением. Отдельное имя проекта не затрагивает dev-том. Ошибка любой миграции завершает контейнер ненулевым кодом и блокирует старт `app`.

## Окружения

Файлы `env/dev.env`, `env/test.env`, `env/stage.env`, `env/prod.env` отличаются портами, числом worker, размерами пулов и уровнем логирования. Для stage/prod сначала соберите один образ и используйте один тег:

```powershell
$env:IMAGE_TAG = "submission"
docker compose --env-file env/stage.env build app migrate
docker compose --env-file env/stage.env up -d
docker compose --env-file env/prod.env up -d --no-build
```

Перед защитой сохраните commit сдаваемой версии и заранее скачайте зависимости и Docker-образы. Не добавляйте в Git `.env`, реальные access-логи, профили и каталог `generated`.
#   l o g - i n s p e c t o r - s t a r t e r  
 