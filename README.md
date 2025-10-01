# Web Downloader Service

A web service that accepts download requests and stores them on disk.

## Quick Start

### Run the server

Change config file `config/app.env` if you want, then:
```
make start
```
Or just:
```
go run cmd/server/main.go
```

### Submit a task

```bash
POST http://localhost:8081/tasks
Content-Type: application/json
Body:
{
  "urls": ["https://ex1.com/1", "https://ex2.com/2"]
}
```

The response will look like this:

```json
{
  "id": "<ID>",
  "status": "pending",
  "files": [
    { "url": "https://ex1.com/1", "status": "waiting" },
    { "url": "https://ex2.com/2", "status": "waiting" }
  ],
  "created_at": "2025-10-01T00:00:00Z",
  "updated_at": "2025-10-01T00:00:01Z"
}
```

## Config

Config is located in `config/app.env`. All fields are optional; defaults are shown below.

| Variable                         | Description                                        | Default               |
|----------------------------------|----------------------------------------------------|-----------------------|
| `SERVER_ADDR`                    | Server address                                     | `:8081`               |
| `GIN_MODE`                       | Gin mode (`release`, `debug`)                      | `debug`               |
| `FILES_DIR`                      | Directory for downloads                            | `./data/server/files` |
| `SEC_DIR`                        | Directory for snapshots and WAL                    | `./data/server`       |
| `USER_AGENT`                     | HTTP User-Agent                                    | Mozilla-like          |
| `DOWNLOAD_TIMEOUT`               | Timeout for each file downloads                    | `2m`                  |
| `RETRY_DOWNLOAD_FILE_MAX_RETRIES`| Number of retry attempts after first failed try    | `5`                   |
| `RETRY_DOWNLOAD_FILE_WAIT`       | Delay between retries                              | `30s`                 |
| `RETRY_ALL_WHEN_QUEUE_FULL_WAIT` | Delay before retrying to enqueue all pending files | `25s`                 |
| `SNAPSHOT_INTERVAL`              | Interval for writing snapshot                      | `6h`                  |
| `SNAPSHOT_TIMEOUT`               | Timeout for writing snapshot                       | `1m`                  |
| `STORAGE_MODE`                   | Storage backend (`memory`, `bbolt`)                | `bbolt`               |


### Storage modes

| Mode      | Persistence model                                         | About                                                        |
|-----------|-----------------------------------------------------------|--------------------------------------------------------------|
| `memory`  | All live in RAM, durability via `snap.json` + `wal.log` | Custom file structure, easy to inspect/debug.                |
| `bbolt`   | All live in `tasks.db`                                  | No snapshots/WAL, CRUD-friendly. Recommended for most cases. |

Note: there is no migrate tool between modes.
Note: in `memory` mode:
- all tasks will be stored both in-memory and on disk;
- even after restart, all tasks will be loaded to memory again, so this mode shouldn't be used for a large number of tasks.

### Data directory

```
data/
└── server/
    ├── files/                # downloaded files per task
    |  └── <task-id>/
    |     └── <filename>
    ├── snap.json             # snapshot
    ├── wal.log               # write-ahead log since last shapshot
    ├── old/                  # old wal files
    └── tasks.db              # bbolt db file 
```

All directories are auto-created if don't exist.


## HTTP API

| Method | Path          | Description                            |
|--------|---------------|----------------------------------------|
| `POST` | `/tasks`      | Create a task from URL list            |
| `GET`  | `/tasks/:id`  | Get status of a task                   |
| `GET`  | `/tasks`      | Tasks list. TODO                       |

### `POST /tasks`

Body:

```json
{
  "urls": ["https://...", "https://..."]
}
```

Response:
- `201` with task.
- `400` if JSON or URLs are invalid

### `GET /tasks/:id`

Returns the full task info + per-file status.


## Architecture

```
┌────────────┐      ┌─────────────┐     ┌────────────────┐
│   Client    │────▶│ Gin Router  │────▶│   Handlers     │
└────────────┘      │  +middleware│     │                │
                    └─────────────┘     └────────────────┘
                           │                       │             
                    RequestID, logging        TaskService
                                                   │                      
                                       (Snapshots + WAL) (Optional)
                                               │
                                       DownloadManager
                                               │
                                             Pool
                                               │
                                               ▼
                                           Downloader
                                               │
                                               ▼
                                             Disk
```


## Shutdown

Server listens for `SIGINT`/`SIGTERM`/`SIGHUP`. On signal:
- `SIGHUP` restarts app: waiting in-progress downloads, but new POST /tasks are accepted anyway (only write tasks for future).
- `SIGINT`/`SIGTERM` works like `SIGHUP`, but app exits.

## Logging

All logs are written in JSON format to stdout and `app_log.log`. Every line includes `request_id`.
