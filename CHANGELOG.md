# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.3.13] - 2026-06-11

A single fix, observed live right after the v1.3.12 deploy.

### Fixed
- **4K files no longer have their content analysis silently skipped on busy storage** ([#339](https://github.com/mescon/Healarr/pull/339)). The content-analysis probe had a hardcoded 30-second ffprobe timeout, while the analysis it gates gets the configurable thorough timeout (default 10 minutes). On a NAS being saturated by a parallel scan, probing a large 4K remux can take longer than 30s, and a timed-out probe skips that file's content analysis entirely, every scan, leaving only a `Content analysis skipped (probe failed)` warning in the log. The probe timeout is now size-aware: files of 4 GiB or more get a 2-minute budget (resolution isn't known until the probe has run, so size stands in for "4K-class"), while smaller files keep the short timeout so a hung probe can't stall a scan worker.

## [1.3.12] - 2026-06-11

A full safety audit of the remediation pipeline (15 verified findings, fixed across seven PRs), two fixes for a live user issue, and a scan-control UX overhaul. The common thread: Healarr must never delete the wrong file, and the UI must show what the scanner is actually doing.

### Fixed
- **Remediation consent can no longer be invented** ([#329](https://github.com/mescon/Healarr/pull/329)). Auto-remediate and dry-run now always resolve from the scan path's *current* configuration (matched against the file's path), never from values embedded in old events. A remediation retried after a config change follows today's config, and when no scan path matches the file anymore, Healarr refuses to delete (treats it as dry-run). The stuck-remediation monitor also respects a user's "ignore" veto instead of resurrecting the remediation.
- **Tool failures can never be classified as file corruption** ([#330](https://github.com/mescon/Healarr/pull/330)). When ffmpeg/HandBrake/mediainfo itself fails (missing binary, OOM-kill, exec error), the verdict is a recoverable infrastructure error, never CorruptStream. A table test pins 15 distinct failure shapes to the safe side, and an unregistered error type now fails loudly in tests instead of silently defaulting.
- **Scan lifecycle integrity** ([#332](https://github.com/mescon/Healarr/pull/332)). Resume progress is persisted from the parallel scanner's contiguous-done watermark (workers complete out of order; the old counter could make a resumed scan silently skip unfinished files), shutdown-vs-cancel races resolve deterministically, a panicking worker no longer hangs the scan, and aborted scans are not resurrected as interrupted.
- **Verification failures are triaged before counting against the retry budget** ([#333](https://github.com/mescon/Healarr/pull/333)). A NAS hiccup during post-remediation verification used to burn a retry (or worse, fail the remediation); recoverable infrastructure errors are now retried with a delay and never emit a terminal event, and verification queries the *arr with the *arr's own path view via the path mapper.
- **Webhook scans wait out unstable files** ([#334](https://github.com/mescon/Healarr/pull/334)). A file whose size is still changing at webhook time (import still copying) is deferred for rescan instead of scanned mid-write, a true-corruption verdict is re-probed once after a stability delay before being trusted, and the duplicate-journey check re-checks the live database, not just a scan-start snapshot.
- **The corruption state machine ignores notification bookkeeping** ([#335](https://github.com/mescon/Healarr/pull/335)). A "remediation complete" notification used to overwrite the corruption's state (knocking it out of the resolved filter forever), and a broken notification provider could exhaust the retry budget by itself. The summary trigger now only follows lifecycle events, retry counting uses one explicit list everywhere, and a migration repairs rows already clobbered. The recurring-corruption loop-breaker is also media-keyed now, so a renamed file (the Tdarr/AV1 scenario) can't evade it, while a deliberate manual retry overrides a paused loop-breaker.
- **Control-plane consistency** ([#336](https://github.com/mescon/Healarr/pull/336)). The dashboard state counts and the /corruptions filters are built from one shared state-to-bucket mapping (nine states used to fall through one view or the other), pause-all/cancel-all act on the statuses scans actually report (they matched nothing before), overlapping scans (a path and its parent/child) can no longer run concurrently, webhook rate limiting no longer starves bursty senders forever (a season-pack import's webhooks used to 429 until restart), and scan retention now actually prunes aborted/interrupted rows.
- **Media lookup works for Windows *arr paths** ([#337](https://github.com/mescon/Healarr/pull/337)). With Radarr/Sonarr on Windows reporting UNC paths (`\\server\share\Movies\...`), the fallback media matcher split paths with platform-locked filepath calls and a hardcoded `/`, so remediation failed with `media not found for path` and the corruption stuck at Deletion Failed. All *arr-path matching now goes through the shared separator-agnostic helpers (fourth and hopefully last member of the separator bug class: [#298](https://github.com/mescon/Healarr/issues/298), [#305](https://github.com/mescon/Healarr/issues/305), [#322](https://github.com/mescon/Healarr/issues/322)). Reported by alex882001 in [#331](https://github.com/mescon/Healarr/issues/331).
- **`/api/health` reports corruptions that still need attention** ([#337](https://github.com/mescon/Healarr/pull/337)). `pending_corruptions` only counted freshly-detected corruptions, so one stuck mid-remediation (e.g. Deletion Failed) reported 0 and homepage dashboards showed all-clear while remediation was failing. It now counts everything not yet resolved or ignored. Also from [#331](https://github.com/mescon/Healarr/issues/331).

### Added
- **Scan controls where the scans are** ([#338](https://github.com/mescon/Healarr/pull/338)). Per-scan Pause/Resume buttons (the API existed since the beginning; no UI ever exposed it) on the Dashboard's Active Scans rows and the Scan Details header, plus Pause all / Resume all / Cancel all on the Active Scans header itself instead of only on the Config page. Pause/resume now also persist to the database, so a paused scan still shows as paused after a reload.

### Changed
- **Scan statuses are consistent and human-readable** ([#338](https://github.com/mescon/Healarr/pull/338)). Pages used to check for the literal status `running`, but a scan's database row advances to `scanning` moments after start (and can sit at `paused`), so a live scan showed a Rescan button instead of Cancel and Scan Details stopped its live refresh. Active-scan detection is now shared across all pages, raw statuses like `enumerating` render as friendly labels ("Counting files"), paused scans get a distinct badge, and the sidebar's Active Scans count updates instantly via websocket instead of lagging up to 30 seconds.

## [1.3.11] - 2026-06-09

Two fixes from a Windows user's testing, a database-bloat fix found along the way, and a docs note.

### Fixed
- **Auto-remediation now finds the *arr instance for Windows UNC paths** ([#323](https://github.com/mescon/Healarr/pull/323)). On a Windows host, remediation failed with `no instance found for path: \\server\share\TV Shows\...` even though the file's corruption was detected correctly. The instance-ownership matcher (`isValidPathMatch`) only trimmed trailing `/` and only treated `/` as a directory boundary, so a Windows *arr's backslash-separated UNC path never matched its configured root folder and remediation gave up. Same separator class of bug as the v1.3.9 webhook fix, in a different matcher; it now accepts `/` or `\` (and `\\srv\media\Movies` still doesn't false-match `\\srv\media\MoviesArchive`). Reported by alex882001 in [#322](https://github.com/mescon/Healarr/issues/322).
- **Manual scans no longer spam "database is locked (SQLITE_BUSY)" warnings** ([#324](https://github.com/mescon/Healarr/pull/324)). Connection-level SQLite pragmas — most importantly `busy_timeout=30000` — were applied with a post-open `db.Exec`, which configures only one connection in the pool; the other three kept `busy_timeout=0` and returned `SQLITE_BUSY` immediately instead of waiting for the lock. The parallel scanner's watermark writer (running alongside the per-file `scan_files` inserts) hit those unconfigured connections and failed noisily. The pragmas now ride on the connection string so every pooled connection gets them, the watermark write uses the same retry wrapper as the other writers, and its best-effort failures log at debug level instead of warn (the watermark is a resume optimization; `scan_files` rows are the source of truth). Reported by alex882001 in [#321](https://github.com/mescon/Healarr/issues/321).
- **The database now reclaims freed space instead of only growing** ([#326](https://github.com/mescon/Healarr/pull/326)). `auto_vacuum` only takes effect on an empty database or after a `VACUUM`, but it was set with a plain `db.Exec` after the schema already existed, so it silently stayed at `NONE`. That made the daily `PRAGMA incremental_vacuum` maintenance a no-op — and since no full `VACUUM` runs in normal operation (only `VACUUM INTO` for backups, which compacts the backup copy), pages freed by retention pruning were never reclaimed and the file only grew. The database is now converted to `INCREMENTAL` auto-vacuum once on startup (a one-time `VACUUM` that also reclaims accumulated free space; skipped on every start thereafter), so incremental vacuum actually works. Found while investigating [#321](https://github.com/mescon/Healarr/issues/321).

### Documentation
- **Windows UNC path note** ([#327](https://github.com/mescon/Healarr/pull/327)). After fixing three Windows-host path issues ([#298](https://github.com/mescon/Healarr/issues/298), [#305](https://github.com/mescon/Healarr/issues/305), [#322](https://github.com/mescon/Healarr/issues/322)), the README "Setting Up Scan Paths" section and the in-app Help "Webhook Path Mapping Errors" troubleshooter now explain Windows *arr setups: set the *arr Path to the UNC form the Windows *arr reports (e.g. `\\server\media\TV Shows`) and the Local Path to where Healarr sees the files; Healarr normalizes the separators automatically.

## [1.3.10] - 2026-06-08

Dashboard accuracy and a webhook log-line nicety, plus a batch of dependency bumps.

### Fixed
- **The dashboard "Last Scan" and per-path "last scanned" now reflect scans that ran but didn't complete** ([#317](https://github.com/mescon/Healarr/pull/317)). The System Overview could show "Never scanned" / "No scans yet" while the `/scans` page clearly listed scans — a confusing contradiction. The cause: the dashboard queried only `status = 'completed'`, so a scan that processed thousands of files before being cancelled or interrupted was ignored. The query now returns the most recent scan that processed at least one file in any terminal state. Scans cancelled during enumeration (0 files) are still excluded since they scanned nothing, and in-flight scans are excluded since they belong in the active-scans indicator. The displayed time is the scan's completion time, or its start time if it was terminated before completing.

### Added
- **Single-file (webhook) scans now log a completion line** ([#318](https://github.com/mescon/Healarr/pull/318)). A webhook-triggered scan logged "Scan started for file" and "Scanning single file" but then ended silently on success. It now logs `Scan completed for file: <path> (healthy)` so a successful scan is visibly confirmed rather than inferred from the absence of an error. Requested in [#305](https://github.com/mescon/Healarr/issues/305).

### Dependencies
- Batch bumps ([#316](https://github.com/mescon/Healarr/pull/316)): `modernc.org/sqlite` 1.51→1.52, `github.com/mattn/go-sqlite3` 1.14.44→1.14.45, `codecov-action` 6→7, `react`/`react-dom` 19.2.6→19.2.7, `@types/react` 19.2.14→19.2.17, `react-router-dom` 7.15→7.17, `vitest` 4.1.6→4.1.8, `@tailwindcss/postcss` 4.2→4.3, `autoprefixer` 10.4→10.5. (Dependabot's react PR bumped react without react-dom; they were re-synced to the same version so the test suite kept passing.)

## [1.3.9] - 2026-06-08

A follow-up to the Windows webhook fixes: mixed-separator paths from a Windows *arr now resolve correctly.

### Fixed
- **Windows Sonarr/Radarr webhooks no longer fail with "no matching scan path" / "invalid argument"** ([#314](https://github.com/mescon/Healarr/pull/314)). v1.3.7's #299 taught the path matcher to *accept* a backslash directory boundary, but the backslashes then survived into the mapped local path — e.g. `/media/Movies\Angels And Demons (2009)\file.mkv`. On a Linux container that broke two things downstream: the scan-path-config lookup (which only treats `/` as a boundary) reported "no matching scan path found", and the filesystem `stat` failed with `invalid argument` because `\` is a literal filename character on Linux, not a separator. Full-library scans were unaffected because they enumerate the Linux filesystem directly and get clean forward slashes; only the webhook path, sourced verbatim from the Windows *arr, carried backslashes. The path mapper now normalizes the mapped remainder to the target path's separator convention in both directions (`ToLocalPath` and `ToArrPath`): backslashes become forward slashes when the target is a Linux path (the container default), and forward slashes become backslashes for a native Windows install. Reported by alex882001 in [#305](https://github.com/mescon/Healarr/issues/305).

## [1.3.8] - 2026-06-05

Makes the scan enumeration phase visible, bounded, and cancellable. Before this, a scan whose directory walk stalled on a slow or unresponsive mount was indistinguishable from a hang.

### Fixed
- **The directory-walk (enumeration) phase of a scan is now visible, time-bounded, and responsive to cancel** ([#303](https://github.com/mescon/Healarr/pull/303)). Previously a scan that was busy enumerating a large or slow path produced a single `Starting scan` log line and then nothing: no scan row existed in the database yet (it was created only *after* enumeration finished), so `/scans` and the dashboard showed nothing while the System Overview reported "no scan"; pressing Cancel did nothing because the walk never checked for cancellation; and there was no timeout, so a genuinely hung mount hung the scan until the container was restarted. Diagnosed live on a mergerfs-over-CIFS mount whose network layer was reconnecting, making each `stat` block for seconds. Four changes: (1) the scan row is created up front in a new `enumerating` state — migration 011 extends the `scans.status` CHECK constraint to allow `enumerating` and `scanning`, two values the scanner code already referenced (`ScanStatusEnumerating`, the reconcile query) but the constraint silently rejected, so they could never persist; the row flips to `scanning` once the walk completes. (2) Enumeration emits a throttled heartbeat (`Enumerating PATH: N media files found so far (M entries scanned)`) every five seconds, so a slow mount is visibly making progress instead of looking dead. (3) The walk now takes a context and checks it on every entry, so a user cancel aborts it promptly and the scan is marked `cancelled`. (4) The walk runs under `HEALARR_SCANNER_ENUMERATION_TIMEOUT` (default 30m); on timeout the scan is marked `aborted` with an explanatory message pointing at slow or unresponsive storage. An orphaned `enumerating` row left by a hard restart reconciles to `cancelled` (its `current_file_index` is 0, so there is nothing to resume).

### Added
- **`HEALARR_SCANNER_ENUMERATION_TIMEOUT`** (default `30m`, env only). Caps the directory-walk phase so a hung network mount can't hang a scan indefinitely. Raise it for very large libraries on slow-but-working storage.

## [1.3.7] - 2026-06-04

Two targeted fixes: the scanner stops doing files in lockstep batches, and Windows users can finally get webhook integration working.

### Fixed
- **Scanner no longer holds back fast files behind slow ones in the same batch** ([#300](https://github.com/mescon/Healarr/pull/300)). The previous design processed files in ordered batches of `scanWorkers` with a `wg.Wait()` barrier between batches: one slow thorough decode held back the other N-1 workers in its batch, and all N completions clustered into the same `CURRENT_TIMESTAMP` second so the scan-detail UI showed identical timestamps for huge chunks at a time. Replaced with a semaphore-bounded worker pool plus a completion bitmap and a contiguous-done watermark ticker. Workers commit their `scan_files` row as soon as detection finishes; a separate goroutine walks the bitmap forward, advances the watermark over the contiguous prefix of done flags, and flushes it to `scans.current_file_index` every two seconds, owning all DB progress writes for the parallel path so the persisted index stays monotonic. Resume after interruption replays from the watermark to the actual highest-completed index, and migration 010 adds `UNIQUE(scan_id, file_path)` so `Record` (now `INSERT ... ON CONFLICT DO NOTHING`) makes the replay window a no-op rather than a duplicate-row error. `scanned_at` also moves to `strftime('%Y-%m-%d %H:%M:%f', 'now')` millisecond precision so per-file order is visible in the UI. Cancellation and pause are checked per dispatch iteration instead of only at batch boundaries. Closes [#290](https://github.com/mescon/Healarr/issues/290).
- **Windows UNC paths from Sonarr/Radarr now match the configured scan path** ([#299](https://github.com/mescon/Healarr/pull/299)). The path matcher used to require the post-prefix remainder to start with a forward slash. When Sonarr or Radarr ran on Windows, every webhook arrived with backslash separators (e.g. `\\srv\share\Movies\Movie (2024)\Movie.mkv`); the `HasPrefix` match succeeded but the `/`-only remainder check rejected it, so every Windows install saw `Webhook path mapping failed: no matching scan path found`. The matcher now accepts either `/` or `\` as a directory-boundary separator, both for the post-prefix remainder check and for stripping trailing separators at load time. `\\srv\share\Movies` still doesn't false-match `\\srv\share\MoviesArchive`. Reported by alex882001 in [#298](https://github.com/mescon/Healarr/issues/298); the v1.3.6 webhook-auth fix ([#293](https://github.com/mescon/Healarr/pull/293)) was a prerequisite for the report — without it the request would have been rejected at the auth gate before ever reaching path mapping.

### Documentation
- **Post-v1.3.6 docs alignment** ([#301](https://github.com/mescon/Healarr/pull/301)). README "Alpine packages" claim corrected (Debian-slim + jellyfin-ffmpeg 8 since v1.3.5), "AV1 NVDEC for Intel iGPUs" replaced with the correct AV1/QSV/Alder Lake or Intel Arc requirement, `docker-compose.yml` mount changed from `/mnt/media` to `/media` to match the convention used everywhere else, `agents/FRONTEND.md` updated to Vite 8 (v1.3.6 bumped it), and the `agents/DATABASE.md` migration table now lists 005-010 instead of stopping at 004.

## [1.3.6] - 2026-06-03

A focused bug-fix release tightening up five rough edges visible in the v1.3.5 production rollout: nested SPA routes serving a blank page, three different numbers shown for the same scan-progress quantity, scans being abandoned on abrupt container restarts even when they had hours of saved progress, the per-instance webhook URL in the UI pointing at the wrong credential (causing Sonarr/Radarr connection tests to fail with 401), and noisy DB lines in the log viewer that looked like errors but were not.

### Fixed
- **Nested routes like `/scans/<id>` no longer render as a blank page** ([#291](https://github.com/mescon/Healarr/pull/291)). The Vite build was configured with `base: './'` (relative asset paths) on the theory that this would support a sub-path mount like `/healarr/`. In practice it broke every nested route: when the browser is at `/scans/1` it resolves `./assets/index.js` as `/scans/assets/index.js`, the Go SPA fallback returns `index.html` for that path, and the browser refuses the import on MIME-type grounds. The "sub-path mount" promise was already broken by SPA routing semantics, so the build now uses an absolute `base: '/'`. Nested routes load assets identically to the root route. Real sub-path mount support would need a build-time env var plus a server-side index.html base-href rewrite and is now explicitly out of scope.
- **The scan-detail page shows one consistent file-scanned count instead of three slightly-different numbers** ([#289](https://github.com/mescon/Healarr/pull/289)). The header progress bar (`Running (X/Y)`), the "Files scanned" stat card, and the "Healthy files" stat card were each computed from a different source at a different cadence: in-memory WebSocket `FilesDone` (per-file), the `scans.files_scanned` column (persisted only every 10 files), and a live `COUNT(scan_files)` (per-file). On a long-running scan you would routinely see e.g. `Running (1855/31107)`, `Files scanned: 1791`, `Healthy files: 1792`. The handler now derives `files_scanned` and `corruptions_found` from the same `GROUP BY status` over `scan_files` that produces the per-status breakdown, so `files_scanned == healthy + corrupt + skipped + inaccessible` by construction. The cached column is kept as a fallback for the unlikely case where the count query errors.
- **Scans that were running when the container was killed abruptly now auto-resume on the next start instead of being marked cancelled** ([#292](https://github.com/mescon/Healarr/pull/292)). When `docker kill` / SIGKILL / OOM-kill / host crash takes the container down, the graceful-shutdown handler that calls `MarkInterrupted` never runs. The startup reconcile (`ReconcileOrphanScans`, originally [#259](https://github.com/mescon/Healarr/pull/259)) caught these orphan `running` rows but marked them all `'cancelled'` (terminal), so a multi-hour scan with thousands of files done was wasted on every hard restart. The reconcile now splits by progress: rows with a saved `file_list` and `current_file_index > 0` are demoted to `'interrupted'` and picked up by the existing `ResumeInterruptedScans` sweep on the same startup. Rows with no resumable state (mid-enumerate, or zero progress) still get cancelled. Both updates run in a single transaction so a partial reconcile cannot leave a row stuck between states.
- **The webhook URL shown in each Arr-instance row now uses the per-instance webhook secret, not the master API key** ([#293](https://github.com/mescon/Healarr/pull/293)). The UI was hardcoded to splice `${apiKeyData.api_key}` into the copy field for every instance. When the instance had a per-instance `webhook_secret` (the default for any instance created after the per-instance-secret migration), the backend correctly rejected the master-key URL with `401 Invalid webhook secret`, but the user had no way to discover the right URL from the UI. Sonarr/Radarr connection tests therefore failed for every freshly-created Healarr instance. The per-row field now prefers `arr.webhook_secret` and falls back to the master api_key only for legacy rows that have no per-instance secret. The Config-page "Webhook API Key" card is renamed to "Master API Key" and reframed as the fallback-for-legacy path it actually is. Closes [#286](https://github.com/mescon/Healarr/issues/286).
- **The in-app log viewer no longer fills with misleading `database is locked (SQLITE_BUSY)` lines** ([#288](https://github.com/mescon/Healarr/pull/288)). Authenticated requests bump `sessions.last_used_at` as a best-effort diagnostic ("when did this session last act?"). Under page-load fanout the frontend fires several parallel API calls that all race on the same UPDATE; SQLite's WAL serializes writers, so all but the winner get `SQLITE_BUSY`. The bump error was already discarded, but the log line at DEBUG level was passed through to the log file and surfaced in the in-app viewer where it read like a real database error. The line is now silenced. No behavior change — the bump was already fire-and-forget.

### Documentation
- **Hardware acceleration setup guide** ([#284](https://github.com/mescon/Healarr/pull/284)). New "Hardware Acceleration" section in the README with ready-to-paste compose snippets for NVIDIA (NVIDIA Container Toolkit + `runtime: nvidia`), Intel QSV (`/dev/dri` device map), and VAAPI (`/dev/dri` + `LIBVA_DRIVER_NAME`). A matching accordion in the in-app Help page covers vendor selection, the `HEALARR_HEALTH_CHECK_HWACCEL` env var, and how to verify hwaccel is engaging by spotting `-c:v <codec>_cuvid` in the live `ps` output. The header sells the actual win: AV1 thorough scans drop from CPU-bound to GPU-bound, taking the per-file decode from tens of seconds to under one.

## [1.3.5] - 2026-06-02

The headline is **GPU hardware decoding actually works now**. v1.3.4 shipped the infrastructure (custom ffmpeg with NVDEC enabled, NVIDIA Container Toolkit passthrough) but missed that `-hwaccel auto` alone doesn't route AV1 / VP9 / VP8 through the GPU — those codecs' default ffmpeg decoders (`libdav1d`, `libvpx`) have no internal hwaccel hooks, so the CUDA context was set up and immediately ignored. This release fixes that with codec-aware decoder selection, switches the base image so the NVIDIA Container Toolkit's library injection actually works, and adds a defensive retry-without-hwaccel safety net so a broken GPU runtime can never trigger mass file deletion. Verified live on an RTX 4070: 30 ffmpeg-on-GPU processes during a scan of an AV1 library, ~224 MiB GPU memory each, captured ffmpeg command line shows `-c:v av1_cuvid`.

Also fixes a three-bug zombie-scan chain that could resurrect cancelled scans across restarts, and a clipped notification provider dropdown.

### Added
- **AV1, VP9, and VP8 thorough scans now actually engage the GPU on NVIDIA, Intel QSV, and VAAPI hosts (Intel/AMD)** ([#281](https://github.com/mescon/Healarr/pull/281)). Healarr probes the input codec via a cheap ffprobe and adds a vendor-appropriate `-c:v` override that engages the actual hardware decoder. Vendor coverage: NVIDIA `*_cuvid`, Intel QSV `*_qsv`, and VAAPI (ffmpeg's bare internal decoder names + `-hwaccel vaapi` hooks). When `HEALARR_HEALTH_CHECK_HWACCEL=auto`, Healarr detects the vendor from `/dev/nvidiactl` (NVIDIA) or `/dev/dri/renderD*` (VAAPI fallback). H.264 / HEVC / MPEG2 / VC-1 don't need explicit overrides because their default decoders already have internal hwaccel hooks. Closes [#276](https://github.com/mescon/Healarr/issues/276).

### Changed
- **Docker base image switched from Alpine + custom-compiled ffmpeg to Debian-slim + jellyfin-ffmpeg** ([#278](https://github.com/mescon/Healarr/pull/278)). On Alpine the NVIDIA Container Toolkit was failing to inject `libcuda.so` / `libnvcuvid.so` into the container despite `runtime: nvidia` being set — every cuvid decoder SIGSEGV'd in 1-2 seconds. The same toolkit works perfectly for jellyfin-ffmpeg on Debian (the build Tdarr uses), so we adopted it. ffmpeg version bumps to 8.1.1-Jellyfin; hwaccel list grows from `cuda/vaapi/drm` to `cuda/vaapi/qsv/drm/opencl/vulkan`. Image size grows ~371 MB → ~908 MB, the cost of a working out-of-the-box NVIDIA path.

### Fixed
- **Thorough decode failures that look like a broken GPU runtime now retry the same file with hardware acceleration disabled instead of letting the failure flow into the corruption classifier** ([#278](https://github.com/mescon/Healarr/pull/278)). The retry catches SIGSEGV, "Failed to setup hwaccel", "Device creation failed", "Cannot load libcuda", and similar decoder-init patterns. Without this guard, a misconfigured driver / missing userspace driver lib / decoder crash would route every affected file straight to the remediation pipeline (delete + re-grab from the *arr) — the silent mass-data-loss failure mode. Healarr now treats GPU-runtime breakage as a transient infrastructure issue, not as evidence of file corruption.
- **Notification provider dropdown no longer gets clipped by its parent card** ([#277](https://github.com/mescon/Healarr/pull/277)). The dropdown was rendered inline with `position: absolute`, which respects ancestor `overflow: hidden` (set on the Settings card for rounded-corner clipping) and Framer Motion's height-animated container. Dropdown now renders via a React portal into `document.body` with `position: fixed` coordinates computed from the trigger's bounding rect, so it escapes all overflow contexts.
- **Cancelling a scan now actually stops the in-process scan loop, and previously-cancelled scans no longer come back as "running" after a restart** ([#275](https://github.com/mescon/Healarr/pull/275)). Three composing bugs were producing zombie scans (see [#274](https://github.com/mescon/Healarr/issues/274)):
  1. `CancelScan` looked up the in-memory scan map by the DB integer id, but the map is keyed by an internal UUID, so the in-memory `ctx.cancel()` never fired. The scan loop kept iterating files after the user clicked cancel — only the DB row was updated. `CancelScan` now also matches by `ScanDBID`, so the HTTP cancel properly signals the in-process scan; `PauseScan` and `ResumeScan` got the same fix.
  2. `ListInterrupted` (the resume-at-startup query) did not filter out rows with `completed_at` set. A scan that was cancelled and later had its status overwritten to `interrupted` by a graceful shutdown was being resumed on the next startup, resurrecting the cancellation as `status='running'`. It now filters on `completed_at IS NULL`.
  3. `MarkCancelled` / `MarkOrphansCancelled` used `WHERE completed_at IS NULL` as the guard against clobbering a real terminal state. That guard also blocked recovery of the inconsistent rows the other two bugs created, leaving them permanently stuck. The guard is now `status NOT IN ('cancelled', 'completed', 'aborted')` — same protection against the cancel-vs-completion race, but also catches the zombie rows.

  Any installs that previously hit this state have rows like `status IN ('running', 'enumerating', 'scanning', 'interrupted') AND completed_at IS NOT NULL` in the `scans` table; the `MarkOrphansCancelled` startup hook will now clean them up automatically on the next restart.

## [1.3.4] - 2026-06-01

The headline of this release is the three-phase **`/config` redesign**: every scan-related setting that was previously env-only is now editable from the UI, can be overridden per scan path, and is bundled into one-click presets. Plus AV1 hardware decode finally works out of the box on NVIDIA hosts.

### Added
- **Scan presets** ([#271](https://github.com/mescon/Healarr/pull/271)). Named bundles of scan settings (detection method, mode, thorough decode duration / timeout, hardware acceleration) that apply to a scan path with one click. Ships with five built-ins: **Zero-byte only**, **Quick** (the existing default), **Fast triage** (decode first 60 s with hwaccel - the "just check the start of every file" preset), **Deep scan** (full decode with a 30-min timeout), and **Paranoid** (HandBrake's stricter decoder, software-only). You can also create your own custom presets under Advanced → Scan presets. Applying a preset writes its values into the scan path form; you can still tweak any field afterwards. Built-in presets are read-only so the dropdown cannot be accidentally emptied.
- **Per-scan-path overrides for thorough decode duration, timeout, and hwaccel** ([#270](https://github.com/mescon/Healarr/pull/270)). Each scan path can pin its own value (NULL = inherit the global). Lets a mixed setup keep one global value while making narrow exceptions: a 4K AV1 library on a CUDA host can force `hwaccel=cuda` and `thorough_duration=60s` while the same Healarr instance scanning a remote SMB share runs with `hwaccel=off` and a longer timeout. Configured under the "Override scanning defaults for this path" disclosure on each scan path.
- **Per-path Dry Run checkbox** ([#270](https://github.com/mescon/Healarr/pull/270)). The `dry_run` column has existed on `scan_paths` for several versions but was missing from the form, so it could only be set via direct DB poke or import/export. It now sits next to the Auto Remediate checkbox.
- **Settings page exposes the runtime tunables that were previously env-only** ([#263](https://github.com/mescon/Healarr/pull/263)). Thirteen `HEALARR_*` knobs are now editable from `/config` (thorough decode duration / timeout, hardware acceleration mode, default retry cap, scanner worker count, scanner shutdown timeout, dry-run mode, retention days, verification timeout / interval, stale threshold, *arr rate limit RPS / burst). Each field shows whether its current value comes from env, the DB, or the built-in default; env-set values render read-only with a "Set by `HEALARR_FOO`" badge so it is obvious which fields are operator-locked.

### Changed
- **Docker image ships a custom ffmpeg with NVIDIA codec support baked in** ([#261](https://github.com/mescon/Healarr/pull/261)). The stock Alpine `apk` ffmpeg was compiled without NVDEC/NVENC/CUVID, so AV1 files always fell back to software decode (`libdav1d`) even on hosts with an NVIDIA GPU passed through. The image now bundles a from-source ffmpeg 7.1.1 built with `--enable-cuda --enable-cuvid --enable-nvdec --enable-nvenc --enable-vaapi`, so AV1 / HEVC / H.264 hardware decode and encode all work when `/dev/nvidia*` (NVIDIA Container Toolkit) or `/dev/dri` (VAAPI / Intel QSV / AMD) is exposed to the container. Runtime ABI shim (`gcompat`) is included so the musl-linked ffmpeg can dlopen the glibc NVIDIA libraries the Container Toolkit injects. Image size: 371 MB (was 249 MB).
- **Thorough scan duration / timeout / hwaccel changes take effect on the next scan without a restart** ([#263](https://github.com/mescon/Healarr/pull/263)). These three values were previously only read at process startup. The internal resolver now consults env > DB > default at every health-check call, so a UI-side change applies immediately to the next file scanned. The other ten tunables still flag as restart-required because they are bound to subsystems (scheduler, rate limiter, retention pruner) that cache their config on startup.

### Fixed
- **Scan-path validation no longer rejects Windows and UNC paths** ([#262](https://github.com/mescon/Healarr/pull/262)). The frontend Zod schema required both `local_path` and `arr_path` to start with `/`, which is wrong for two cases: (1) Healarr running on Windows directly (the binary, not the Docker image), and (2) Healarr running on Linux while talking to a Sonarr / Radarr that itself runs on Windows and returns paths like `D:\Media\Movies` or `\\server\share\Movies`. Both fields now accept POSIX absolute (`/foo`), Windows drive-letter (`C:\foo`, `c:/foo`), and UNC (`\\server\share`, `//server/share`) forms. Fixes [#255](https://github.com/mescon/Healarr/issues/255).
- **Hardware acceleration probe no longer claims success on hosts that only expose an emulated VGA** ([#261](https://github.com/mescon/Healarr/pull/261)). When `HEALARR_HEALTH_CHECK_HWACCEL=auto`, the probe used to accept any ffmpeg whose build advertised hwaccels, even inside a VM where the only "GPU" is QEMU/Bochs emulated VGA (PCI vendor `0x1234`) with no decode hardware behind it. Healarr would then add `-hwaccel auto` to every command, ffmpeg would silently fall back to software, and AV1 thorough checks would time out. The probe now also verifies that at least one credible GPU device is exposed to the container (`/dev/nvidiactl`, or a `/dev/dri/renderD*` whose sysfs vendor is not `0x1234`) and logs a clear warning otherwise so the misconfiguration is obvious from the logs.

### Removed
- **Dead `health_check_mode` column on `scan_paths`** ([#270](https://github.com/mescon/Healarr/pull/270)). It was defined in `001_schema.sql` with a CHECK constraint but never read or written by any Go code; the concept moved into `detection_mode` (which is what the scanner actually consults). Carrying both invited future confusion. Migration `008` drops the column.

## [1.3.3] - 2026-05-29

### Added
- **Thorough health checks are now tunable for slow codecs (AV1) and large files.** Three new env vars:
  - `HEALARR_HEALTH_CHECK_THOROUGH_TIMEOUT` (default `10m`) - raise this if scans repeatedly time out and end up parked in the rescan queue.
  - `HEALARR_HEALTH_CHECK_THOROUGH_DURATION` (default `0`, no limit) - when set (e.g. `60s`), the thorough decode walks only the first N seconds via ffmpeg `-t`. Catches header errors, codec-init errors, decode errors at the start, and files that will not open at all; trades completeness for speed.
  - `HEALARR_HEALTH_CHECK_HWACCEL` (default `auto`) - opportunistic ffmpeg hardware acceleration. Probes `ffmpeg -hwaccels` once and adds `-hwaccel auto` if any accelerator is reported; falls back silently to software on hosts without one. `off` to disable, `<name>` (e.g. `cuda`, `vaapi`, `videotoolbox`) to force a specific one.

### Fixed
- **Cancelling a scan now actually cancels it.** Previously `CancelScan` only signaled the in-memory `ctx.cancel()` and never persisted the new status, so /scans and Dashboard kept showing the row as "running" on reload (and the "Scan cancelled" toast was misleading). For stale "running" rows left over from a previous hard restart - where there is nothing to signal in memory - the cancel button did literally nothing (a 404 from the handler). Cancel now always writes `status='cancelled'` with `completed_at` to the DB, so both live cancels and stale-row cleanups work. The "AND completed_at IS NULL" guard prevents a benign cancel-vs-completion race from clobbering a real completion.
- **Stale "running" scan rows from a previous hard restart are no longer shown forever.** On startup the scanner now reconciles: any row left in an active status (`running`/`enumerating`/`scanning`) that this process did not start was orphaned by a `SIGKILL`/OOM/crash that prevented the normal `MarkInterrupted` shutdown path from running. Those rows are now marked cancelled at startup with `error_message='abandoned on Healarr restart'`. `paused` and `interrupted` are left alone (legitimate resumable states).
- **`/corruptions` with the "All" filter no longer crashes with a database scan error.** The notifier was publishing every `NotificationSent`/`NotificationFailed` with a hardcoded `aggregate_type="corruption"`, so notifications fired for non-corruption events (e.g. a Pushover alert for `SystemHealthDegraded`) leaked into `corruption_summary` as stray rows with `NULL file_path`. Loading the corruptions page with no filter included them and tripped a `converting NULL to string` scan error. The notifier now propagates the source event's aggregate type, defaults defensively to `"notification"` if missing (never `"corruption"`), and a migration cleans up any rows already polluted while tightening the `corruption_status` view as belt-and-braces.

## [1.3.2] - 2026-05-28

### Added
- **Unmonitored items are now re-acquired during remediation.** If a corrupt file belongs to an item the *arr has unmonitored (for example a transcode pipeline like Tdarr unmonitored it to protect its output), Healarr now temporarily monitors it so the *arr can grab and import a healthy replacement, then restores the original monitored state. Previously such an item was deleted but never re-acquired, leaving you with nothing.
- **Remediation loop-breaker.** If the same file keeps coming back corrupt even after being restored to health several times (the signature of a transcode pipeline or failing storage re-corrupting it, which re-downloading cannot fix), Healarr now pauses auto-remediation for that file and raises a needs-attention notification instead of repeatedly deleting and re-downloading it. It resets automatically once the file stays healthy.

## [1.3.1] - 2026-05-27

### Added
- **Parallel scanning**: file detection now runs across a worker pool, so large libraries scan substantially faster. The default worker count is tuned to the memory available to the container so a constrained host won't be pushed into an out-of-memory kill; override with `HEALARR_SCANNER_WORKERS` (1 to 32).
- Configurable shutdown grace period for in-flight scans via `HEALARR_SCANNER_SHUTDOWN_TIMEOUT` (default 30s).
- Defense-in-depth HTTP security headers on all responses (`X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`).
- **Corrupt releases are now blocklisted instead of re-grabbed.** When a downloaded replacement is itself corrupt, Healarr deletes it, marks that specific release as failed in Sonarr/Radarr so the same release is not grabbed again, and lets the *arr fetch the next-best release. The original corruption still gets one grace re-search first (a single bad download is not always the release's fault); blocklisting kicks in only once a replacement comes back corrupt. Bounded by the path's retry limit, after which the item is flagged for attention.
- **Stalled downloads are now blocklisted too.** When a replacement never finishes downloading and times out, Healarr removes it from the download client and blocklists that release, so the re-search grabs a different release instead of waiting on the same dead one. Honors per-path auto-remediate / dry-run and is bounded by the retry limit.
- Modal dialogs now trap keyboard focus and expose proper ARIA roles, improving keyboard and screen-reader navigation.

### Changed
- Faster database writes during scans: SQLite now uses `synchronous=NORMAL` under WAL (still corruption-safe), and per-file scan-progress updates no longer hit the durable event store.

### Fixed
- **Files on a temporarily-unavailable mount or network share are no longer mistaken for corruption**, preventing deletion of healthy files when a mount drops mid-scan. Mount/network errors are now classified by OS error code, so this also holds on non-English systems.
- **Deletion now refuses to act on an ambiguous match.** If the corrupt file can't be identified unambiguously in the *arr media (for example two files share a name), or the owning *arr instance is misconfigured, the remediation fails and is retried instead of risking deletion of a healthy file. An exact path match is preferred, with a unique-basename match as the only fallback.
- **Per-path `auto_remediate` and dry-run settings are now honored on retries and on startup recovery.** Previously a corruption re-triggered after a restart could delete a file on a path configured as manual or dry-run; the remediator now re-reads the path's current settings before acting.
- **A file that is not tracked in its *arr no longer produces a fake "already deleted" success.** Previously, on setups where the *arr sees media at a different mount path than Healarr, an untracked file could be reported as deleted, causing a search to be triggered while the corrupt file remained. The remediation now fails honestly and retries.
- **Startup recovery of an interrupted deletion now uses a thorough (stream-level) health check** instead of a quick header-only check, so a file with stream corruption is no longer prematurely marked resolved.
- **Re-search now refuses to fall back to a whole-series search.** When the specific episode cannot be identified, Healarr no longer issues a series-wide search that could re-download every missing episode; the attempt is surfaced instead.
- **Active-corruption lookups no longer collide across sibling library roots.** A path like `/media/TV` previously matched `/media/TV-Archive`; lookups now require a path boundary.
- **A verified-corrupt replacement is now actually re-remediated.** Previously, a retry after a failed verification only re-searched without removing the corrupt file; because the *arr will not replace a file that is still present, the item could stall. The retry now deletes the corrupt replacement before re-searching.
- **A replacement that never finishes downloading no longer retries forever.** Repeated download timeouts now count toward the path's retry limit, so an item that can never be satisfied escalates to a needs-attention state instead of re-searching indefinitely. (The retry count shown in the UI still reflects corruption failures only.)
- Live log and scan-progress updates no longer drop messages under load (WebSocket delivery rework).
- A paused scan could fail to resume if the resume signal raced with the scan loop; resume is now reliable.

### Security
- The authentication token is no longer written to the browser console on WebSocket connect.

## [1.3.0] - 2026-02-19

### Added
- **Content Analysis Detection**: Detect corruptions that pass basic integrity checks
  - Black video detection: finds files where video is entirely black frames
  - Frozen video detection: finds files where video is a single still frame
  - Silent audio detection: finds files with no audible audio track
  - Configurable per scan path alongside existing detection modes

### Security
- **API Hardening**: Strengthened endpoint protection across the board
  - Rate limiting now applied to all protected API routes
  - Metrics endpoint (`/metrics`) now requires authentication
  - API error responses no longer expose internal Go type information (29 handlers fixed)
  - CORS wildcard mode now sets `Vary: Origin` to prevent cache poisoning
  - Setup dismiss endpoint now rate-limited like other setup endpoints

### Improved
- **Accessibility**: Active scans table rows now fully keyboard-navigable
- **Code Quality**: Drove SonarCloud to zero issues across all categories
  - 0 bugs, 0 vulnerabilities, 0 security hotspots, 0 code smells
  - Reduced cognitive complexity in several critical functions
  - 84.7% test coverage maintained throughout

### Dependencies
- Bumped modernc.org/sqlite, quic-go, prometheus, golang.org/x/net, protobuf
- Bumped lucide-react, framer-motion, tailwindcss, recharts, axios, msw, typescript-eslint

## [1.2.1] - 2026-02-12

### Improved
- **Code Quality Enforcement**: Lint and type checking are now required to pass in CI
  - Previously these checks ran but failures were silently ignored
  - Pull requests with lint or type errors will now be blocked until fixed
- **Cleaner React Patterns**: Replaced several state-syncing effects with derived state
  - "How to Update" section now expands automatically when an update is available, without extra re-renders
  - Configuration warning banner loads faster by reading session state upfront
  - Base path and About section settings initialize without flicker

### Dependencies
- Bumped axios, typescript-eslint, eslint-plugin-react-refresh, react-router-dom

## [1.2.0] - 2026-02-07

### Added
- **Form Validation**: Configuration forms now check your input before saving
  - Clear error messages shown next to each field
  - Validates *arr instances, scan paths, and notification settings
- **Crash Recovery**: If something goes wrong in the UI, you get a friendly error screen with a retry button instead of a blank page
- **Monitoring Metrics**: Prometheus-compatible metrics endpoint for Grafana or similar monitoring stacks
  - HTTP request duration and count
  - Database query performance
  - WebSocket connection tracking
- **Keyboard Navigation**: Notification provider dropdown and dashboard cards fully support keyboard-only navigation
- **Screen Reader Support**: Improved accessibility throughout the UI
  - Skip navigation link for keyboard users
  - Dashboard cards use proper button semantics
  - Dropdown menus use ARIA listbox pattern

### Fixed
- **Gotify/ntfy Notifications Broken**: Fixed "Please fill in all required fields" error when adding Gotify or ntfy notification providers, even though all fields were filled correctly (#105)
- **Container Restart Hangs**: Healarr could hang when restarting the Docker container if a verification was in progress
  - Verifier service now included in graceful shutdown sequence
- **Background Tasks Leaking**: Database maintenance tasks now stop cleanly on shutdown instead of continuing to run in the background
- **Error Messages Exposing Internals**: API error responses no longer show raw Go type information
  - All handler error messages sanitized

### Improved
- **Docker Security**: Container now runs with `no-new-privileges` and drops all Linux capabilities by default
- **Server Timeouts**: HTTP server now enforces read (15s), write (30s), and idle (120s) timeouts to prevent connections from hanging indefinitely
- **Setup Wizard**: Split into smaller, faster-loading step components for a smoother first-run experience
- **Rate Limiting**: Config import and database restore endpoints during setup are now rate-limited to prevent abuse
- **System Info**: The system info endpoint now requires authentication

### Dependencies
- Upgraded Zod from v3 to v4 for faster form validation
- Upgraded Vitest from v3 to v4
- Bumped framer-motion, lucide-react, recharts, jsdom, @types/node
- Bumped Go dependencies (SQLite driver, crypto, validator)

## [1.1.33] - 2026-01-13

### Added
- **New Notification Events**: Three additional events for better monitoring
  - "Stuck Remediation" - When a remediation hasn't progressed for too long
  - "Arr Instance Healthy" - When a previously unreachable *arr instance recovers
  - "Corruption Ignored" - When you manually dismiss a detected corruption
- **Docstring Coverage Enforcement**: CI now validates code documentation
  - Ensures all exported functions and types are documented
  - Currently at 100% coverage
- **Stuck Remediation Recovery**: Automatic retry when remediation is stuck
  - HealthMonitorService detects stuck items and triggers immediate retry
  - Prevents items from sitting idle indefinitely

### Fixed
- **Stability Improvements**: Fixed several race conditions that could cause hangs
  - Scan progress updates no longer conflict with shutdown operations
  - Health check timeouts handled more gracefully
  - File verification counters now thread-safe
  - Verification goroutines now properly cancelled on retry (prevents duplicates)
- **Memory Leak**: Fixed gradual memory growth from retry timers
  - Retry timers now properly cleaned up after firing
  - Long-running instances stay lean
- **Duplicate Scanning Prevention**: Files scanned via webhook no longer re-scanned during bulk scans
  - Prevents wasted processing when webhook and scheduled scan overlap
- **Near-Complete Download Detection**: Improved handling of downloads at 99%+ progress
  - Verifier retries history API multiple times before marking as ManuallyRemoved
  - Handles timing delays where import appears in history after queue clears

### Improved
- **Graceful Shutdown**: New scans blocked during shutdown to prevent hangs
  - In-progress scans complete cleanly before exit
  - No more stuck shutdown states
- **Test Coverage**: Comprehensive tests for all concurrent code paths
  - 85%+ coverage on services package
  - All race conditions have corresponding test cases

## [1.1.32] - 2026-01-11

### Added
- **Retry All Button**: Manual intervention banner now has a "Retry All" button
  - Previously mentioned clicking "Retry here" but no button existed
  - Bulk retry functionality for all items needing manual intervention
- **Episode Titles**: TV shows now display episode titles in the format "Series S01E08 - Episode Title"
  - Richer context for identifying specific episodes
- **Event Replay Service**: Unprocessed events are now replayed on startup
  - Fixes race condition where events published just before restart weren't processed
  - Ensures CorruptionDetected events are delivered to remediator after restart

### Fixed
- **False ManuallyRemoved State**: Items no longer incorrectly marked as removed when import succeeds
  - Checks for import events in history before marking as ManuallyRemoved
  - Handles NFS sync delays and path mapping timing issues
- **Recovery Service State Coverage**: All intermediate states now recovered on startup
  - Previously missed `RemediationQueued`, `DeletionStarted`, `DeletionCompleted` states
  - Items stuck in early remediation states are now properly reprocessed
- **Lost Retry Timers**: Retry schedules are now preserved across restarts
  - Failed states (`SearchFailed`, `RemediationFailed`, etc.) trigger retry on startup
  - Items at max retries correctly transition to `MaxRetriesReached`

### Improved
- **EventBus Buffer Monitoring**: Warning logged when subscriber buffer is full
  - Events are still persisted to DB (not lost), warning aids debugging
- **Semaphore Timeout**: Remediator semaphore now has 2-minute timeout
  - Prevents indefinite hangs if HTTP calls get stuck
  - Emits failure event on timeout for proper retry flow
- **Verifier Concurrency Limit**: Maximum 50 concurrent verification goroutines
  - Prevents resource exhaustion during bulk scans
  - 5-minute timeout with appropriate failure events

## [1.1.31] - 2026-01-10

### Fixed
- **Mobile Table View**: Fixed tables showing mobile card view instead of desktop table
  - Tailwind JIT now correctly compiles responsive breakpoint classes
  - Scans and Scan Details pages display proper tables on desktop
- **Filter Dropdown**: Fixed filter disappearing when no results match
  - Filter controls now stay visible regardless of filtered result count
  - Users can always change filters even with zero matching items
- **Database Query Stability**: Fixed flaky "context canceled" errors during queries
  - QueryWithRetry no longer cancels context prematurely
  - Prevents intermittent failures when iterating database results

## [1.1.30] - 2026-01-10

### Improved
- **Code Organization**: Refactored Config page into modular section components
  - ArrServersSection, ScanPathsSection, SchedulesSection, NotificationsSection
  - Each section is self-contained with its own React Query hooks
  - Easier to maintain and extend individual configuration areas
- **Confirmation Dialogs**: Replaced browser alerts with animated modal dialogs
  - Supports danger, warning, and info variants
  - Focus management and keyboard navigation (Escape to close)
  - Loading state support during async operations
- **Skeleton Loaders**: Added loading placeholders for better perceived performance
  - DataGrid shows skeleton rows while loading
  - Smoother transitions when data is fetching
- **Accessibility**: Improved keyboard navigation and screen reader support
  - Dialogs trap focus and support Escape key
  - Better ARIA labels throughout the UI

## [1.1.29] - 2026-01-10

### Fixed
- **WebSocket Stability**: Fixed disconnect/reconnect on every menu navigation
  - Connection now stays stable during route changes
  - Only reconnects when actually disconnected or token changes

### Improved
- **Tux Icon**: Converted to inline SVG matching Docker, Apple, and Windows icons
  - Uses `fill="currentColor"` for proper CSS color inheritance
  - Smaller bundle size (no external file needed)
  - Consistent styling across all platform icons

## [1.1.28] - 2026-01-10

### Fixed
- **Config Import in Wizard**: Fixed redirect to /login when importing JSON config
  - Uses authenticated endpoint when user has a token
  - Public endpoint still works during initial setup
- **Duplicate Prevention**: Importing config no longer creates duplicate entries
  - Skips arr instances with matching URL
  - Skips scan paths with matching local_path
  - Skips schedules with matching path + cron expression
  - Skips notifications with matching name
- **Logo Display**: Removed green gradient background from logo containers
  - SVG logo now displays without decorative background
  - Cleaner appearance in sidebar, login, and wizard
- **README.md Logo**: Increased logo size to 96px with vertically centered text
- **Setup Wizard Reset**: Fixed wizard not appearing after using "Reset Setup Wizard"
  - Wizard now correctly shows after reset, skipping password step if already set
  - Allows users to reconfigure arr instances, paths, and notifications

## [1.1.27] - 2026-01-10

### Added
- **Full Notification Support in Setup Wizard**: All 21 notification providers now available
  - Same feature parity as the Config page
  - Provider selection with icons and categories
  - Event selection for which notifications to receive
  - Test notification button with result feedback
- **Restore Pre-population**: Wizard fields now pre-fill after config/database restore
  - Shows "Restored: X instances, Y paths, Z notifications" banner
  - Values can be reviewed and modified before saving
- **Shared Notification Components**: Reusable components for Config and Wizard
  - ProviderSelect, ProviderFields, EventSelector, ProviderIcon
  - Consistent UI across both pages

### Improved
- **Updated Logo**: New healarr.svg logo in sidebar, wizard, and login pages
- **README.md**: Logo now displayed beside "Healarr" title with matching height

### Fixed
- **Setup Wizard Navigation**: Going back after setting password no longer causes error
  - Previously showed "Setup already completed" when clicking Continue
  - Now correctly skips to next step if password already set
- **Subdirectory Deployment**: Fixed all absolute asset paths for reverse proxy support
  - All icons now use `import.meta.env.BASE_URL` prefix
  - Works correctly with HEALARR_BASE_PATH (e.g., /healarr/)
  - Fixes broken notification icons when deployed behind a reverse proxy subdirectory

## [1.1.26] - 2026-01-09

### Added
- **Setup Wizard Reset**: Re-run the setup wizard anytime from the Config page
  - Useful if you skipped steps during initial setup
  - Access via Config → General Settings → Reset Setup Wizard
- **Smart Instance Naming**: Arr instances now get friendly auto-generated names
  - First Sonarr instance named "Sonarr", second becomes "Sonarr 2", etc.
  - Works for Sonarr, Radarr, and Whisparr

### Improved
- **URL Validation**: Clearer error messages when adding arr instances
  - Explicitly tells you if URL is missing http:// or https://
  - Better feedback for malformed URLs
- **Test Coverage**: Comprehensive testing across all packages
  - Added tests for pagination, path validation, and instance naming
  - Database package now at 80%+ coverage
  - Overall test coverage improved to ~84%

### Fixed
- **Setup Wizard File Upload**: Fixed file selection not working in restore section
  - Clicking "Click to select .db file" or ".json file" now properly opens file picker
  - Added hover effects for better visual feedback

## [1.1.25] - 2026-01-09

### Added
- **Mobile-Friendly Tables**: All data tables now adapt to mobile screens
  - Tables collapse into expandable cards on phones and tablets
  - Tap to expand and see all details
  - Works on Dashboard, Corruptions, Scan Details, and Configuration pages
- **Skipped & Inaccessible Files**: Scan details now show why files weren't checked
  - New stat cards for Skipped and Inaccessible file counts
  - Filter by status to see exactly which files had issues
  - Helps identify permission problems or unsupported file types

### Improved
- **Setup Experience**: Better feedback during configuration
  - Progress indicators when testing connections
  - Clearer error messages when things go wrong
  - Path validation shows file counts before saving
- **Error Handling**: More helpful messages throughout the app
  - Network errors show retry options
  - Server errors explain what went wrong
  - Validation errors highlight exactly what to fix
- **Performance**: Faster loading on large libraries
  - Scan details load with fewer database queries
  - Path validation limits file scanning to prevent timeouts

### Fixed
- **Logs Page Scroll**: Fixed auto-scroll jumping to bottom when viewing older logs
  - Auto-scroll now pauses when you scroll up
  - Resumes automatically when you scroll back to the bottom

## [1.1.24] - 2026-01-09

### Improved
- **CI/CD Quality**: Fixed code quality pipeline configuration
  - SonarCloud now correctly excludes test utilities from coverage calculations
  - Quality gate checks pass consistently on all pull requests
- **Test Coverage**: Additional edge case testing
  - Media details lookup now thoroughly tested (movie and series)
  - File accessibility checks have improved coverage

## [1.1.23] - 2026-01-09

### Improved
- **Reliability**: Major internal code refactoring for improved stability
  - Simplified complex code paths in authentication, notifications, database maintenance, and file scanning
  - Easier to debug and fix issues faster in future releases
- **Test Coverage**: Expanded automated testing to 85% coverage
  - More bugs caught before they reach users
  - Better confidence in updates and new features

## [1.1.22] - 2026-01-08

### Improved
- **Reliability**: Internal code improvements for better stability
  - Cleaner error handling throughout the application
  - Better error logging helps diagnose issues faster
- **Test Coverage**: Added tests for the recovery system
  - Stale item detection now thoroughly tested
  - Recovery workflows validated automatically

## [1.1.21] - 2026-01-08

### Security
- **Server Protection**: Strengthened security against common web attacks
  - *arr instance URLs now validated to prevent malicious redirects
  - Directory browser locked down to prevent unauthorized file access
  - Database backup cleanup hardened against path manipulation
  - Frontend protected against open redirect attacks

## [1.1.20] - 2026-01-06

### Added
- **First-time Setup Wizard**: Guided onboarding for new users
  - Step-by-step wizard walks you through initial configuration
  - Choose between fresh start, import existing config, or restore backup
  - Test *arr connections with real-time feedback
  - Auto-detect library paths from your *arr instances
  - Skip option available for power users
- **Database Restore**: Restore from backup directly in the UI
  - Upload a previous backup to restore your configuration
  - Automatic safety backup created before restore
  - Validates backup integrity before applying

### Fixed
- Fixed crash when importing configuration without path mappings

## [1.1.19] - 2026-01-06

### Fixed
- **Upgrade Fix**: Fixed upgrade failure for some users coming from older versions
  - Database migration no longer fails on corrupted or incomplete data
  - Gracefully handles edge cases from previous versions

## [1.1.18] - 2026-01-06

### Added
- **Custom Tool Paths**: Use your own versions of detection tools
  - Set custom paths via environment variables for ffprobe, ffmpeg, mediainfo, HandBrake
  - Or simply place binaries in `/config/tools/` folder
  - Useful for users needing newer codec support

### Changed
- **Updated Tools**: Alpine Linux 3.23 with newer media tools
  - ffmpeg 8.0.1 (was 6.1.1)
  - HandBrake 1.10.2 (was 1.6.1)
  - MediaInfo 25.09 (was 24.04)
- **Database Reliability**: Major improvements to prevent data loss
  - Safer backup mechanism with integrity verification
  - Better crash protection for unexpected shutdowns
  - Improved performance for corruption status lookups

## [1.1.17] - 2026-01-06

### Security
- **Input Validation**: Protected sorting options against manipulation
  - Only allowed sort fields accepted by the API

### Fixed
- **Scan Progress**: Progress now shows correctly (was off by one file)
  - "100/100" now means actually finished, not still processing
- **Database Reliability**: Fixed several edge cases where errors could be silently lost
- **Retry Handling**: Better validation before retrying failed items
- **Error Messages**: Clearer error messages throughout the application

## [1.1.16] - 2026-01-06

### Added
- **Live Scan Progress**: Watch scans happen in real-time
  - Progress bar with file count on scan details page
  - See which file is currently being scanned
  - Files table updates automatically during scan
- **Running Scan Count**: Active scans now show progress in status badge

### Fixed
- **Real-time Updates**: Fixed WebSocket events not updating the UI
  - Dashboard now updates instantly when scans progress
  - Corruption list refreshes automatically on state changes
  - All notification events now properly reflected in UI

## [1.1.15] - 2026-01-05

### Added
- **Tool Detection**: Healarr now checks for required tools on startup
  - Shows which tools are installed and their versions
  - Warning banner when required tools are missing
  - Tool status visible in System Information
- **About Section**: Version and system info now on Help page too
- **Friendly Event Names**: Notification events now show readable names
  - "ScanStarted" displays as "Scan Started" with helpful description
- **Better Update Instructions**: Detailed, step-by-step update commands
  - Docker, Linux, macOS, and Windows instructions
  - Includes tool installation for each platform

### Fixed
- **Matrix Icon**: Matrix notification icon now visible in light mode

## [1.1.14] - 2026-01-05

### Fixed
- **Health Monitor**: Instance health checks now work correctly
  - Previously failed with confusing "no instance found" error
- **Provider Icons**: Notification providers now show proper icons
  - 18 provider icons: Discord, Slack, Telegram, Pushover, and more
- **Config Import/Export**: Schedules and notifications now transfer correctly
- **Status Badges**: Long status text no longer wraps awkwardly

### Added
- **Stuck Remediation State**: New orange status for items stuck over 24 hours
  - Helps identify items that need manual attention

## [1.1.13] - 2026-01-05

### Fixed
- **Performance**: Fixed slowdowns when background services were busy
  - API now responds quickly even during heavy scanning
  - Database queries have proper timeouts to prevent hangs
- **Database Speed**: Added optimizations for faster file lookups
  - Significant improvement on large libraries

## [1.1.12] - 2026-01-04

### Fixed
- **API Responsiveness**: Fixed endpoints hanging when database was busy
  - Health check now always responds (important for Docker)
  - Corruptions page loads reliably under heavy load

## [1.1.11] - 2026-01-04

### Added
- **Rich Media Information**: Corruptions now show friendly titles
  - See "Colony S01E08" instead of raw file paths
  - *arr instance icons for quick identification
  - File size and download progress displayed
- **Enhanced Remediation Details**: See the full download journey
  - Download client, protocol (Usenet/Torrent), and indexer shown
  - Visual progress bar during downloads
  - Quality badges (4K, 1080p, 720p) on completion
  - Release group tags for easy identification
- **Duration Tracking**: See how long remediations take
  - Download time and total resolution time displayed

### Fixed
- **Version Display**: Docker builds now show proper version numbers

## [1.1.10] - 2026-01-04

### Added
- **Auto-Recovery on Startup**: Healarr now recovers gracefully after restarts
  - Checks *arr for current status of in-progress items
  - Automatically resolves items that completed while Healarr was down
  - Marks abandoned items appropriately
- **Periodic Sync**: Checks *arr status every 30 minutes
  - Catches missed webhooks and state drift
  - Self-heals when things get out of sync
- **"No Replacement Found" Status**: New distinct state when search exhausted
  - Clearly different from verification failures
  - Allows unlimited manual retries
- **Configurable Stale Threshold**: Control when items are considered stuck
  - Default 24 hours, adjustable for slow download clients

## [1.1.6] - 2026-01-02

### Fixed
- **Startup Reliability**: Fixed potential hang during scheduler startup (#8)
  - Added timeouts to prevent indefinite waiting
  - Cleans up orphaned schedules from deleted scan paths
  - Better error messages for troubleshooting

## [1.1.5] - 2026-01-02

### Fixed
- **Connection Lost**: Fixed "Connection Lost" error on root deployments
  - API calls no longer break when not using a subpath

## [1.1.4] - 2026-01-02

### Fixed
- **Reverse Proxy Login**: Fixed login redirect ignoring base path (#6)
  - Now correctly redirects to `/healarr/login` when using subpath

## [1.1.3] - 2026-01-02

### Fixed
- **Docker Permissions**: Fixed PUID/PGID being ignored on Unraid (#5)
  - Container now properly runs as specified user
- **Add Server Button**: Fixed silent failure when adding *arr servers (#1)
  - Now shows clear error messages and success confirmations
- **Duplicate Events**: Fixed excessive event spam for blocked imports
- **Notification IDs**: Fixed incorrect IDs in notification events

### Added
- **Manual Intervention Alert**: Banner when items need your attention

## [1.1.2] - 2026-01-01

### Fixed
- **Base Path Assets**: Fixed static files not loading with custom base path

## [1.1.1] - 2026-01-01

### Improved
- Internal code organization and test coverage improvements

## [1.1.0] - 2025-12-31

### Added
- **Resilient Connections**: Automatic recovery when *arr instances go offline
  - No more cascading failures from temporary outages
  - Automatic reconnection when services come back
- **Comprehensive Testing**: Major test coverage improvements
  - All API handlers thoroughly tested
  - Service layer fully covered
  - Platform-specific error handling validated
- **Better Pagination**: Consistent paging across all list views
  - Page size configurable, default 50 items

### Changed
- Minimum Go version updated to 1.25
- Improved error handling throughout the application

## [1.0.3] - 2025-12-02

### Added
- **Manual Intervention Detection**: Know when *arr needs your help
  - Detects blocked imports (quality cutoff, existing files)
  - Detects when you manually remove items from queue
- **Dashboard Improvements**:
  - New "Manual Action" card for items needing attention
  - Click active scans to view details
  - Click stat cards to filter the file list
  - Scan duration/elapsed time display
- **Notification Support**: Get notified when manual action is needed

## [1.0.2] - 2025-12-01

### Fixed
- **Path Support**: Fixed rejection of valid Radarr/Sonarr naming patterns
  - Curly braces `{imdb-tt0848228}` now work correctly

## [1.0.1] - 2025-12-01

### Security
- Fixed potential command injection vulnerability in health checker

### Changed
- Simplified notification system internals
- Improved corruption detection handler

### Added
- Expanded test coverage for scanner operations
- Performance benchmarks for critical operations

## [1.0.0] - 2025-11-28

### Added
- **Initial Release**
- Detect corrupted media files using ffprobe, MediaInfo, or HandBrake
- Works with Sonarr, Radarr, and Whisparr
- Automatic healing: delete corrupt file and trigger re-download
- Real-time progress updates via WebSocket
- Dashboard with statistics and charts
- Per-path settings: auto-remediate, dry-run mode, retry limits
- Quick and thorough detection modes
- Scheduled scans with cron expressions
- Instant scanning via *arr webhooks
- Notifications: Discord, Slack, Telegram, Pushover, Gotify, ntfy, Email
- Import/export configuration and database backups
- Dark and light themes
- Password-protected with API key authentication
- Docker images for amd64 and arm64

