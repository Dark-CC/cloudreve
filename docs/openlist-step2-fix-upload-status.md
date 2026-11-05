# Upload stuck at "uploading": root cause and fix

## What the user saw

A file dropped into the test instance (`230x0w.png`) finished transferring but
stayed labelled **uploading** (上传中) forever. The list showed it at size 0.

## Two independent faults, both fixed

### 1. The old instance's data directory had been deleted outside the app

The running instance was started as `/tmp/crdist/cloudreve -c /tmp/cr15299/conf.ini`,
with its SQLite database, its upload directory and its own binary all living
under `/tmp/cr15299/`. That directory no longer existed. The process kept
serving from memory — the site loaded, login worked — but every operation that
touched the database failed:

```
unable to open database file: out of memory (14)      (227 occurrences)
failed to persist Task into DB: failed to create task: unable to open database file: out of memory (14)
```

SQLite reports a missing directory as `out of memory`, which is what made this
look like a memory or connection problem. It was not: the process held only 7
file descriptors against a limit of 1048576, and `/` had 60 GB free.

Clearing the upload session that marks a file as "uploading" is a database
write, so it could never happen. The file was left as a size-0 placeholder with
its `sys:upload_session_id` metadata still set.

The database was unrecoverable, so the instance was rebuilt on the persistent
disk at `/data/cr15299new/` (see *Deployment* below).

### 2. `CompleteUpload` panicked when the owner's settings were absent

Rebuilding the instance did not make the symptom go away — a fresh upload was
still left at size 0 with its session metadata intact. The server log showed:

```
[Recovery] panic recovered:
runtime error: invalid memory address or nil pointer dereference
  pkg/filemanager/fs/dbfs/upload.go:405
  pkg/filemanager/fs/manager/upload.go:554
  service/explorer/upload.go:223
```

`CompleteUpload` read the owner's version-retention policy directly:

```go
owner := filePrivate.Owner()
if entityType == types.EntityTypeVersion &&
    owner.Settings.VersionRetention && ...
```

Both `owner` and `owner.Settings` are nullable — the settings column is
nullable and the owner edge can be missing from a partially loaded file. The
panic escaped the handler *after* the last chunk had been stored, so the request
returned 200, the placeholder file survived, its `sys:upload_session_id` was
never cleared, and the UI kept showing "uploading". Every other reader of these
two fields in the package already nil-checked them (`dbfs.go:876`, `dbfs.go:882`,
`file.go:208`, `manage.go:319`); this one did not.

**Fix** (`pkg/filemanager/fs/dbfs/upload.go`): nil-check both before reading the
retention policy. Absent settings now fall through to the pre-existing default
of `maxVersions = 1` instead of panicking.

### 3. Empty path rules on a storage policy (hardening)

A policy stored with both `dir_name_rule` and `file_name_rule` empty expands to
an empty save path. Every non-local policy type received these rules only from
the administrator's wizard, which fills them in client side, so a policy created
through the API, a script or a migration could reach the database with both
rules empty.

**Fix** (`pkg/filemanager/fs/dbfs/dbfs.go`, `service/admin/policy.go`,
`frontend/.../StoragePolicySettingWrapper.tsx`):

- `DefaultDirNameRule = "uploads/{uid}/{path}"` and
  `DefaultFileNameRule = "{originname}"` are substituted when a stored rule is
  empty (`dirNameRule()` / `fileNameRule()`).
- `expandNameRule` falls back to `uri.Name()` if the expansion is blank or still
  contains a separator.
- Policy creation applies the same defaults server side.
- The admin wizard ships the defaults and trims empty submissions.

This was not the cause of the reported symptom on the old instance, but it is
the same class of "upload completes, file is unusable" fault and is cheap to
close.

## Verification

Backend:

```
go build ./...                       OK
go vet ./pkg/filemanager/fs/dbfs/    OK
go test ./pkg/filemanager/fs/dbfs/   ok  (includes the new regression tests)
vite build                           OK (pre-existing type errors only)
```

Live, against the rebuilt instance:

| upload | size | metadata after completion |
| --- | --- | --- |
| `probe.png` | 69 | `{}` — clean |
| `final-check.png` | 69 | `{}` — clean |
| `230x0w.png` (after restart on the fixed binary) | 69 | `{}` — clean |

No `panic recovered` entries after the fixed binary was started. Before the fix
the same sequence left `size = 0` with
`sys:upload_session_id` still present, which is exactly the "uploading"
symptom.

Regression tests added in `pkg/filemanager/fs/dbfs/upload_owner_settings_test.go`.

## Deployment

The instance that serves the public address is now:

| item | value |
| --- | --- |
| directory | `/data/cr15299new/` (persistent disk) |
| database | `/data/cr15299new/data/cloudreve.db` |
| uploads | `/data/cr15299new/bin/data/uploads/` |
| binary | `/data/cr15299new/bin/cloudreve` (previous: `cloudreve-old.bak`) |
| config | `/data/cr15299new/conf.ini`, listening on `:15299` |
| console log | `/data/cr15299new/console3.log` |

`:15299` is the internal port that the public address maps to, so the browse
and login URLs are unchanged. Nothing was placed under `/tmp` this time, so the
failure that started this investigation cannot repeat.

The old process (pid 1822071, running on a deleted directory) was stopped.

## Instance state

The database could not be recovered, so the instance was re-initialised: the
file index is empty and the administrator account had to be recreated. The
uploads shown above are the verification files and can be deleted.

## Related issues seen but not changed

- The earlier "import prefix `cloudreve://`" and "SQLite write-lock" reports
  were not reproduced here and share no code with these fixes.
- The local policy's upload root resolves relative to the executable
  (`/data/cr15299new/bin/data/uploads`). It lives on the persistent disk, so it
  is safe, but it would follow the binary if the binary were relocated.
