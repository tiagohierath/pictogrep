# Pictogrep desktop, session tasks

Started 2026-09-06. Calendar view, app header, folder page, drag-to-scroll.

Rules for this file: every task Tiago gives goes in here BEFORE work starts on
it. Numbers are permanent, they are never reused or renumbered. A task moves
between sections, it does not change its number.

## Open

| # | Task | State |
|---|------|-------|
| 21 | Cannot click the Pinterest import text box | needs Tiago to retest |
| 22 | "Site has control of your pointer" | needs Tiago to say where he sees it |
| 27 | Cannot click text boxes on the import screen | needs Tiago to retest |
| 20 | Drawing drops areas inside images | needs Tiago to describe it |
| 28 | Remove Pinterest branding, accept any link | needs a decision, then work |
| 29 | Make "Full width" the default for new users | ready to do |
| 30 | Dragging is still native inside an open picture | ready to do |
| 33 | Ship the macOS version | ready to do, plan already written |
| 34 | Identicons for folders and sync devices | ready to do, needs a look decided |
| 35 | Two blank buttons in the Android build | DONE 2026-09-08 |
| 36 | Cut the link importer out of the Android build | DONE 2026-09-08 |
| 37 | Sync a library desktop to phone | DONE 2026-09-08, untested on a phone |
| 38 | Make all plugins free on desktop; mobile keeps paid plugins | DONE 2026-09-08, commit 0661041 |
| 39 | Follow-up: "make all plugins be like on every install" | DONE 2026-09-08, folded into 38 |
| 40 | Cut a new GitHub release | DONE 2026-09-08, tagged v0.11.9, CI running |
| 41 | Update the local install with this session's build | DONE 2026-09-08 |
| 42 | Drastically improve the storyboard and canvas ("Área livre") plugins | investigated, needs a decision |
| 43 | Check on Pictogrep usage tracking that reports to navylily.tv | CHECKED 2026-09-18, working; follow-ups below |
| 44 | Install event, so activation rate has a denominator | DONE 2026-09-18, NOT deployed |
| 45 | Report: activation + weekly retention, on a page he can open | DONE 2026-09-18, NOT deployed |
| 46 | Sessions per user and core-action counters | NOT DOING, see 44-45 |
| 47 | Back up the usage database | DONE 2026-09-18 |

### 20. Drawing drops areas inside images

Reported 2026-09-06: "drawing works not well like inside images, areas
missing". First guess was that drag-to-scroll was stealing the strokes. That is
WRONG: drawing lives in `web/practice.html`, which loads neither `app.js` nor
`app.css` and has its own pointer handling (`setPointerCapture`,
`getCoalescedEvents`). So this is pre-existing and unrelated to this session's
work. Still needs investigating on its own.

BLOCKED: needs Tiago to say which tool and what "areas missing" looks like.

### 21. REGRESSION: cannot click the Pinterest import text box

Reported 2026-09-06: "importing from pinterest stopped working, cannot click on
text box". Prime suspect was this session's drag-to-scroll, most likely the
capture-phase click swallow after a pan, or a stuck `body.page-panning` class
leaving `user-select: none` behind.

NOT REPRODUCED 2026-09-06. In Firefox against a fresh build, the board link box
takes a real mouse click and typed text, with focus landing on
`#pinterestBoardURL`; a text box clicked straight after a pan also focuses and
types, `body.page-panning` is not left behind and `body` holds no pointer
capture. The likeliest cause was task 22's `setPointerCapture` on
`document.body`, which retargets pointer events while a drag is live, and that
is now gone.

BLOCKED: needs Tiago to try it again and say whether it is still broken.
See also task 27, which is this reported a second time.

### 22. Turn off "Site has control of your pointer"

Reported 2026-09-06. Firefox shows this for the Pointer Lock API, but nothing
in `web/` calls `requestPointerLock`. The only pointer API in play is
`setPointerCapture`: mine on `document.body` in the pan (removable, the
document-level listeners already cover it) and pre-existing ones in
`practice.html` (NOT removable, strokes need to continue outside the canvas).

Mine is removed. The drag is carried by the document-level listeners alone, and
leaving the window simply ends it, which is what letting go means anyway.
Verified `document.body.hasPointerCapture(1)` is false after a pan.

BLOCKED: if Firefox still shows the message it is `practice.html`, so this
needs Tiago to say where exactly he sees it.

### 27. REGRESSION: cannot click text boxes

Reported 2026-09-06, after the 0.11.7 release and the local 1.5 gain build:
"clicking on stuff is BUGGED, you can't click on boxes like to put a link from a
pinterest folder". Task 21 again, and this time it is not going away on its own.
Not reproduced by a synthetic pointer drag against a real gesture (down, six
20px moves, up) on the search box, a text box right after dragging a picture,
or the Pinterest board link box: all three stayed clickable and typeable.

Then Tiago: "maybe disable drag-scroll on some important windows IDK fix it",
then: "fix, its mostly bugged on the import from pinterest screen". Went with
the suggestion rather than keep chasing a repro: drag-to-scroll is now off
entirely inside `#drawer` and `#pluginSidebar`. That panel is packed with small
controls close together (radio cards, checkboxes, labels wrapping their own
input), so a drag that starts a few pixels off one of them could still be read
as meant for it; the panel already scrolls fine on its own, so panning there was
not worth the risk.

BLOCKED: needs Tiago to confirm the Pinterest screen works now.

### 28. Remove Pinterest branding, accept any link

Requested 2026-09-06. Same task filed in `pictogrep-android/TODO.md` as its
task 1; whatever this lands on is what Android mirrors.

- Drop the Pinterest name, icon and red accent (`#bd081c`) from the import UI:
  `#showPinterest`, `#pinterestSection`, the `.pinterest-*` classes in
  `web/app.css`, and the `pinterest.*` strings in both locale files. Reframe as
  a general "import from a link" flow.
- `importPinterestBoard()` in `web/app.js` currently rejects anything whose
  hostname does not match `pinterest.\.` and needs at least 2 path segments.
  Accept any URL; `web_source.go` / `native_gallery.go` already have a general
  web importer to compare against and possibly fold into.
- Check `pinterest.go`, `pinterest_sync.go`, `native_gallery_pinterest.go`,
  `native_gallery_pinterest_mobile.go` for anything Pinterest-specific in the
  backend that would need to generalize too, not just the front end.

BLOCKED on a decision: does this replace the existing "Import from a web page"
flow (`webSection` / `web.*`) entirely, since they would do the same job? Ask
Tiago before writing any of it.

### 29. Make "Full width" the default for new users

Requested 2026-09-06 ("total width largura total be the default option for new
users"). The Full width grid setting from task 12 defaults to off; new users
should start with it on. Existing users' saved choice must not change under
them, so the default has to apply only when nothing is stored, not as a
migration that overwrites.

### 30. Dragging is still native inside an open picture

Reported 2026-09-06: "also that still can drag on images after you open them".
Task 24 made `loadImage()` set `image.draggable = false`, which covers every
picture in the grid, but the full-size image inside `#imageViewer` is not
created there. Find where the viewer builds its `<img>` and fix it the same
way.

### 42. Drastically improve the storyboard and canvas ("Área livre") plugins

Requested 2026-09-08. Investigated the current state in both this repo and the
private `pictogrep-plugins-paid` repo before touching anything.

**Canvas ("Área livre").** Two implementations exist right now.
- Core still runs the old one: `canvas.go` (73 lines) plus roughly two dozen
  call sites woven into `web/app.js`. Pan, zoom, move one picture at a time,
  autosave. That is what desktop actually shows today.
- `pictogrep-plugins-paid/canvas/` is a separate, already-built "2D board"
  plugin (manifest version 0.1.0, `board.js` at 834 lines): pan/zoom,
  library tray, marquee multi-select, resize, rotate, flip, duplicate,
  layering, undo/redo, tidy-to-grid, fit, focus, mouse/trackpad/touch/
  keyboard. The paid repo's own README calls it "the first plugin here with
  a complete standalone interaction model." It is not wired into Pictogrep
  yet: not copied into any `pluginsDir`, not in the installed-plugins list.

**Storyboard.** One implementation, unfinished.
- Core's version is real and free today: `GET /practice`
  (`server.go:106`), a 2000-line standalone page (`web/practice.html`),
  linked from the main menu.
- `pictogrep-plugins-paid/storyboard/` is a 91KB `ui/index.html` scaffold,
  manifest version `0.0.0`. `loadPlugins` in `plugins.go` skips any manifest
  at `0.0.0` on purpose ("do not turn private design placeholders into
  broken buttons"), so this cannot even load yet. The paid repo's own README:
  "Storyboard has a substantial extracted UI but still depends on
  core-only APIs" that were never built.

So "drastically improve" lands on two very different jobs: canvas is
"replace the woven-in core implementation with the already-finished plugin
and remove the old one," while storyboard is "finish an incomplete
extraction, including designing whatever core capability it's still
missing." Per task 38, paid plugins are free on desktop and gated only on
mobile, so wiring either one in makes it immediately usable on desktop with
no unlock flow to build first.

BLOCKED on a decision: which of the two to do, and in what order, since
canvas is close to a straight swap and storyboard is open-ended design work
(what core API it needs is not written down anywhere). Asked Tiago.

DECIDED 2026-09-08: canvas first. Two more decisions, asked before writing
code:

- **One board per folder, not one global board.** The plugin as built has a
  single fixed `STORAGE_KEY`, i.e. one board for the whole library. Tiago
  wants today's model kept: opening a folder's canvas gives that folder its
  own saved board. Implementation choice (not asked, mechanical): the
  library tray stays global (search/add from anywhere, which is the actual
  improvement this plugin brings), only the saved node layout and camera are
  keyed per folder, via `STORAGE_KEY + ":" + scope` where scope is
  `tag:<name>` or `source:<path>`, the same two shapes `canvasScope()`
  already uses server-side. Storage stays a flat plugin-scoped `storage.kv`
  file, no core capability change needed.
- **Needs Portuguese.** The plugin's UI text is hardcoded English, not wired
  to Pictogrep's i18n system. Since a plugin's CSP is `connect-src 'none'`
  (see `plugins.go`'s `servePlugin`), it cannot fetch a locale JSON file the
  way the host page does; strings have to ship inlined in the plugin's own
  files. The host passes the current locale in as `?lang=` on the iframe
  `src` (`PictogrepI18n.locale()`), the same place `?scope=`/`&label=` carry
  which folder this board belongs to.

Root cause of a bug Tiago hit while this was being investigated: "images on
the board are like, same aspect ratio, super silly". That was the CURRENT
Folder canvas (`.canvas-image` in `web/app.css`), which draws every picture
into a fixed 112x92 box with `object-fit: cover`, i.e. crops every picture to
the same shape. It is not a bug worth patching separately: this whole
implementation is what's being deleted. The new plugin already computes each
card's box from `image.width`/`image.height` (`createNode()` in `board.js`)
and never crops (`object-fit: contain`), so it's fixed by the swap itself,
not by an extra change.

Found while verifying the android-tagged suite: `go test -tags pictogrep_android
./...` was never actually finishing. `TestFolderCanvasPositionsPersistWithoutMovingImages`
(one of the old canvas tests, now deleted along with the feature) panicked on
a nil type assertion under that tag and took the whole test binary down with
it, mid-run, silently. Every test declared after it in build order never ran
at all, which is how the "26 failures" figure in this file's own Traps
section was measured all along, on a suite quietly truncated at 153 of 235
tests. Deleting that test fixed the crash as a side effect, and the suite now
runs to completion at 235/235, surfacing 8 real pre-existing failures that
had simply never had the chance to run: `TestWebImportFollowsOnlyWhenAsked`,
`TestWebImportListsExistingLibraryImageInTheFolder`,
`TestWebImportNeedsAtLeastOneChoice`,
`TestWebImportSurvivesThePinterestPluginBeingOff`,
`TestFollowedWebSourceIsRecheckedAndCanBeForgotten`,
`TestReCheckWithNothingNewIsNotAFailure`, `TestRunningJobSaysWhichPanelStartedIt`,
`TestSyncRefusesATamperedFolder`. All eight assume `setPluginEnabled("web",
true)` succeeds on a phone build; task 36 made that compile-time impossible
(`offersWebImport = false` in `platform_mobile.go`) and these tests were
never updated for it. Not fixed here: unrelated to canvas/storyboard, and
worth its own look rather than a rushed patch. The real, trustworthy android
baseline going forward is 235 run, 8 failing, all of them this one stale
cluster; the "26" figure below is superseded.

IN PROGRESS.

### 43. Check on Pictogrep usage tracking that reports to navylily.tv

Asked 2026-09-18: "pictogrep, im tracking usage on navylily.tv" / "check on it".

IT WORKS. Checked end to end, both halves.

- Client: `usage.go` POSTs one anonymous event per active calendar day to
  `https://navylily.tv/api/pictogrep/active-day`. Desktop only
  (`tracksDailyUsage`, false in `platform_mobile.go`).
- Server: `navylily-private/auth/pictogrep_usage.go`, writing to
  `auth/data/pictogrep-usage.db` (gitignored, not tracked, so no user data has
  ever gone into the repo). Endpoint is live: a bad event gets a 400, so the
  handler is wired and answering, not 404 or 502.

What is in the store on 2026-09-18: 60 active-day rows, 9 installations,
2026-08-24 to today. 6 on 0.11.9, 2 on 0.11.7, 1 on 0.11.3; 6 Windows, 3 Linux.
Active today 2, last 7 days 9, last 30 days 9, nobody active all 7 of 7.

THE THING WORTH KNOWING: the whole 25-day history survived the VPS outage
because of the client's offline queue, not because anything was restored. The
database file was created fresh on the laptop 2026-09-16 11:45 and the backup at
`~/backups/navylily-vps-20260818` holds no usage database at all. Every row's
`recorded_at` is 2026-09-16 or later, arriving in four backlog dumps, one per
install, each spanning weeks of `date` values: 22 rows at 09-16 19:00 covering
08-24 to 09-16, then 4, 11 and 15 more on 09-17 as the other installs next ran.
`flush()` never drops a pending date until the POST returns 2xx, so months
offline cost nothing. This is the design working, and it is worth not breaking.

WHY IT MATTERS, said 2026-09-18: "i got like 400 downloads in some days but if
people don't use then i will just abandon the project." So the 9 above is being
read as a kill signal. It is not one, and here is why.

**THE 9 CANNOT BE COMPARED TO THE 400. Three separate reasons, each fatal on
its own.**

1. **The 400 is one release, and that release has no tracking code in it.**
   The spike is `v0.8.6`, published 2026-08-17: 322 installer downloads, 284 of
   them the Windows setup. `usage.go` landed 2026-08-23 in commit `cd3aca8`,
   six days later. Verified against the tags rather than assumed: `git cat-file
   -e v0.8.6:usage.go` fails, `v0.9.0` on is fine. Every one of those 322 could
   be running Pictogrep daily and the server would never hear a word.
2. **Only 95 of 476 lifetime installer downloads are from a build that can
   report at all.** The other 381, 80% of everything, are pre-tracking 0.7/0.8.
3. **The server never recorded a single event before 2026-09-16.** Proven, not
   guessed: the installs backfilled dates as old as 08-24, and `flush()` never
   resends a date that already got a 2xx. If the VPS had ever acked those days
   they would not have been in the queue. The 2026-08-18 backup holds no usage
   database either. So the receiver was dead for the entire window being judged.

Net: the real denominator for those 9 is "someone on a 0.9+ build who still had
it installed and used it on or after 2026-09-16", not 400 and not 476. And one
of the 9 is Tiago's own laptop (`bbf34577`, the only `usage.json` on this
machine, 23 of its 25 days), so the outside number is 8 at most.

**What the survivors actually look like, which is the one trustworthy part:**
23 of 25 days, 15 days, 11 days. All 9 active in the last 7 days. That is
habitual use, not tire-kicking. Small sample, good shape.

STILL OPEN:

- **Launch and use are indistinguishable today.** `usage.json` is written when
  the app starts, but nothing is sent until a meaningful action fires
  `reportMeaningfulActivity()` (search, open a picture, open a folder, upload,
  create a folder). So "downloaded, opened it once, bounced" and "never
  downloaded" look identical from the server. That gap is exactly the question
  being asked, so a separate first-launch event would turn download -> launch
  -> use into a real funnel. Highest-value change here by a distance. NOT
  STARTED, needs Tiago's go-ahead.
- **`/api/pictogrep/report` has no page.** It computes summary, retention
  cohorts (D1/D7/D30, 7-of-7, 30-of-30) and GitHub download counts, is
  owner-gated by `isOwner`, and returns JSON that nothing renders.
  `public/pictogrep.html` is the product page, not a dashboard. So the numbers
  above are only reachable by hand.
- **The usage database has no backup.** One file, on the laptop, gitignored by
  design. Client backlogs would NOT refill it: an install only resends dates it
  never got a 2xx for, so anything already acked is gone if the file is. It is
  now the only cohort that exists, so losing it costs the whole decision.
- **The tunnel is the laptop.** See the lid-switch rule. A sleeping laptop does
  not lose events, clients queue them, but it does freeze the report.

### 44-47. Fix the trackers (asked 2026-09-18)

"yeah fix the trackers to what you find most important", then the goal, which
decides every choice below: "my goal is to get the info of whether i should
focus or not in the project. if people genuinely use it every week for months,
then its probably worth more polishing."

So the question is WEEKLY retention over MONTHS. Not downloads, not DAU. That
reframes the existing report: `Active7Of7` and `Active30Of30` count CONSECUTIVE
daily use, which for a reference-image tool will read 0 essentially forever, and
a permanent 0 staring back at him is worse than no metric. They go. What
replaces them is a weekly cohort table: of the installs that first showed up in
week N, how many were active in week N+1, N+2, ... That is the shape that
answers "still using it months later".

Nothing here can be answered today no matter what I build: the receiver has only
worked since 2026-09-16 (task 43), so week 1 of real data ends 2026-09-22. This
work is about having the instrument right and unbroken between now and then, so
that in December the answer is there to read.

SCOPE, cut down on Tiago's "KEEP IT SIMPLER, SIMPLE keep it super super super
simple" after the four metrics were asked for. Of the four, three are already
answerable from the `active_days` rows that exist today and need NO client
change at all, only better queries:

- D7 / D30, do they come back: query.
- Weekly retention over months: query.
- Sessions per user, how often: approximated by active days per install, which
  is the same table. A true session counter needs new client state, so it is
  NOT being built. Task 46, deliberately not done.
- Core actions (search / import / organize / export): the only one that would
  need real new instrumentation, and the one furthest from the focus-or-drop
  decision. NOT being built either.

That leaves exactly one thing that new code must provide.

#### 44. Install event

THE GAP: `usage.json` is written when the app starts, but nothing reaches the
server until a meaningful action fires `reportMeaningfulActivity()` (search,
open a picture, open a folder, upload, create a folder). So "downloaded, opened
it, bounced" is invisible and looks exactly like "never downloaded". Activation
rate has no denominator without this, and activation is the number that says
whether the problem is reach or the product.

- Client sends one install event carrying `InstallationCreated`, the date
  already in the state file. Existing installs therefore backfill their TRUE
  install date on upgrade rather than faking a new one.
- Its own endpoint and its own table, so old clients posting the old shape keep
  working untouched.
- Install failure must NOT block active-day flushing, and vice versa.

#### 45. Report and a page to read it on

Drop `Active7Of7` / `Active30Of30`: consecutive DAILY use, which reads 0 forever
for a tool like this, and a permanent 0 is worse than no metric. Same objection
to the existing D1/D7/D30, which test `date = cohort + 7 days` exactly, so
somebody who used it on day 6 and day 8 counts as gone. Both become
range-based: came back at all within days 1-7, and within days 8-30.

Add the weekly cohort table, which is the actual question. Page follows the
`funil.html` pattern (owner-only, 404 to everyone else, `no-store`), but NOT its
`.muted` class: that is lightened ink and this repo's rule is weight, size or
accent instead.

#### 47. Back up the database

`VACUUM INTO` a dated copy. One line, no timer, no service.

#### DONE 2026-09-18. NOT DEPLOYED, waiting on Tiago.

Both repos build, both test suites at their pre-existing baselines: navylily/auth
fully green, pictogrep at its documented 10 Pinterest/import failures, no new
ones. Nothing is committed, nothing is pushed, the running site is still the old
binary.

- `pictogrep/usage.go`: `InstallReported` in the state file, `flushInstall()`
  posting once to `/api/pictogrep/install`, `flushActiveDays()` unchanged in
  behaviour and now independent of it. Three tests: reported without any
  activity, reported only once, and an existing state file backfilling its real
  install date instead of today.
- `navylily-private/auth/pictogrep_usage.go`: `installs` table (created by
  `CREATE TABLE IF NOT EXISTS`, verified applying cleanly to the real file),
  `handlePictogrepInstall` sharing decode/validate/rate-limit with the active-day
  handler via `readPictogrepEvent`, plus `readPictogrepActivation` and
  `readPictogrepWeeks` in the report.
- `public/pictogrep-uso.html` at `/pictogrep/uso`, owner-only, 404 to everyone
  else, excluded from the sitemap.
- `README.md` corrected: it claimed events are only sent after you use the app,
  which stopped being true the moment the install event existed.
- Backup: `~/backups/pictogrep-usage/pictogrep-usage-20260918.db`, 61 rows.

BUG FOUND AND FIXED while verifying: a week was counted as elapsed ON its last
day rather than after it, so the most recent week of every curve would have been
measured while people still had hours left to open the app, shaving it downward
every single time the page was loaded. Now strictly `today.After(to)`.

Checked at 390px in Firefox against a stubbed report: the table fits without
sideways scroll once the bar column is dropped under 34em, all four columns
readable, no gray ink anywhere.

WHAT THE REAL DATA SAYS TODAY, run against a copy of the live database: weeks 0,
1 and 2 are all 100%, on 4, 4 and 2 installs respectively. Everyone who got far
enough to be measured kept coming back. N is tiny and only 3 weeks deep, so it
is an encouraging shape and not yet an answer. Activation reads 0 of 0 until
clients carrying the install event are actually out there, which needs a release.

### 33. Ship the macOS version

Requested 2026-09-08 ("also add mac version"), promoting the plan below from
"later" to work to actually do. The plan is already written to
`docs/macos-port.md` (2026-09-07); follow it rather than re-deciding it.

Settled already, do not relitigate: skip Apple's 99 USD/year developer
programme and ship a terminal install, so Gatekeeper never fires on an unsigned
binary. `GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build ./...` is verified to
succeed as-is, so this is packaging and install flow, not a port of the code.

Open questions to settle with Tiago before or during the work:

- arm64 only, or a universal build that also covers Intel Macs?
- Does the release script build it locally alongside the Linux artifacts? It
  must, per the no-CI rule, and that means a cross-build on the Linux box.
- Does auto-update work on macOS, or does the terminal install replace it?

No Mac hardware here, so anything beyond "it builds and the install script is
right" needs Tiago to run it on a real machine and report back.

### 34. Identicons for folders and sync devices

Requested 2026-09-08. A generated mark, derived from the name or id, so a
folder without a cover picture and a paired device in the sync list are told
apart by shape and colour instead of by reading the label.

Two places, one generator:

- **Folders.** A folder with no cover currently falls back to whatever the
  folder card shows when it is empty. The identicon fills that, and is
  replaced by a real cover the moment one is set.
- **Sync devices.** The paired-device list is text only, so two phones read as
  two identical rows. Applies to Android as well, which draws the same web UI,
  so file the mirror in `pictogrep-android/TODO.md` once the look is settled.

Undecided, ask before building: what the mark actually looks like. GitHub-style
symmetric pixel blocks are the obvious cheap answer but sit badly next to the
app's flat brutalist cards. Alternatives worth putting in front of Tiago:
initials on a hashed background, or a small geometric mark. Whatever it is, it
must be drawn in the client from a hash, with no request and no stored image,
and honour the no-gray-text rule for any letterform in it.

### 37. Sync a library from a desktop to a phone

Implied by Tiago's 2026-09-08 description of the split: "gallery-dl / other
importers handle the disgusting internet-scale acquisition problem. User can
dump hundreds or thousands of images into Pictogrep. Pictogrep organizes and
indexes them locally. Android receives/syncs an existing Pictogrep library."

THE GAP: sync only runs phone to desktop. `sync_controls.go:190` says it
outright, "a desktop cannot push to a phone at all", and the outbox tests are
all named for the same direction. So today a phone cannot receive the library a
desktop acquired, which is exactly the half Tiago describes as the point of the
Android app.

This matters more once task 36 lands, because sync becomes the ONLY way a large
library reaches a phone. Until then the phone's answer to "how do I get my
pictures here" is the share sheet, one picture at a time.

DESIGNED 2026-09-08, decisions made rather than asked, on Tiago's "yeah do it".

**It is a PULL, not a push.** The name of the task is misleading and the code
should not follow it. Reachability is asymmetric on purpose: the phone dialled
the desktop when it scanned the QR, so the phone holds an address that answers
(`peer.Listens`) and the desktop holds a source port that nothing can be sent
to. A push would need hole punching, a relay, or a listener on the phone, all
of which are new problems. A pull needs none: the phone asks the desktop what
it has and takes it, over the connection it already knows how to open.

It is also the better answer for the phone. The device with the small disk, the
metered radio and the battery is the one that should decide what arrives and
when.

**The protocol mirrors what exists.** Sending is `POST /manifest` (here are my
hashes, which are you missing) then `POST /blobs/{hash}`. Receiving is the same
two ideas turned around, on the same authenticated TLS listener:

- `POST /catalogue` on the desktop: what do you have. Answers hash, name and
  folder for each picture, optionally filtered to one folder.
- `GET /blobs/{hash}` on the desktop: the bytes.

Everything reuses what is already there: the pinned certificate, `peerClient`,
the digest cache that makes hashing a library cheap, `libraryIndex()` for
"what do I already have", and `saveImportedImageWithOptions` for the import,
which dedupes by the same content hash the manifest already speaks.

**Decisions, so nothing here is a surprise:**

- **User asks for it.** Not automatic, not on a timer. A phone that silently
  filled itself with 3419 pictures over someone's mobile data would be a bug
  however well it worked.
- **A folder at a time, with "everything" available.** The phone lists the
  desktop's folders and their counts and takes what is chosen.
- **Originals, not thumbnails.** The phone indexes what it holds and searches
  it, so a downscaled copy would be a different library, not a cheaper one.
- **Nothing is ever deleted.** A pull only adds, on both sides. Matches what
  the rest of sync already promises.
- **Skipped, not failed, when it is already there.** Same content hash, same
  answer as the outbox gives.

**Scope of this pass:** the two endpoints, the puller, the phone's API and a
control on its sync screen, and tests. Not in scope: scheduling, deletion,
two-way reconciliation, or bringing folders and tags across as structure rather
than as a destination folder.

DONE 2026-09-08, commit d0e7f4d.

- `sync_catalogue.go` answers: `POST /catalogue`, `GET /blobs/{hash}`.
- `sync_pull.go` asks, and imports what comes back.
- `GET /api/app/sync/library`, `POST /api/app/sync/get`, and
  `/api/app/sync/get/stop` on the phone's own server; progress rides along in
  the `GET /api/app/sync` the panel already polls.
- The control is phone-only, on the Connect screen. Pictures land in a folder
  named after the one they came from, so taking three folders keeps them apart.

VERIFIED. Six tests in `sync_pull_test.go`, against two real devices on a real
socket, covering: pictures cross, what the phone already has is skipped by
hash, a named folder takes only that folder, the catalogue leaks no path from
the computer's disk, a hash the library does not hold is not servable, and a
second pull is refused while one runs. Desktop suite at its pre-existing 10
failures, android-tagged at its pre-existing 26.

Checked at 390px in Firefox: the section appears only when a reachable computer
exists, select and button are both 44px, no sideways scroll, progress line is
full ink. That last one needed `.sync-get .sync-get-progress`, because
`.drawer-section p` is 0,1,1 and was dragging it to `--fg-muted`.

NOT VERIFIED ON A PHONE. No hardware here. Belongs in the on-device pass in
Android task 2, and it is the biggest thing in it now.

STILL NOT DONE, and worth knowing before this is called finished:

- **No progress on the desktop side.** A computer being read from says nothing
  and shows nothing.
- **Folders arrive as a destination, not as structure.** Pulling "trips" makes
  a "trips" folder on the phone; it does not reproduce nesting or tags.
- **Nothing is scheduled.** Every pull is asked for. That is the decision above,
  not an omission, but it means a phone does not stay up to date on its own.

### 36. Cut the link importer out of the Android build

Requested 2026-09-08: "i think i'll keep galery-dl on desktop, and remove from
the android so it doesnt do anything." Filed on Android as its task 6, where
the policy research behind the decision is written out.

WHY. `offersPinterest = false` already compiles the board reader out of the app
build, and `platform_mobile.go` gives the reason: Play's Device and Network
Abuse policy forbids using a service "in a manner that violates its terms of
service", and Pinterest's terms forbid automated collection. The general link
importer that replaced that panel in the UI does the same thing to every other
site: `runNativeGallery` sweeps a page for up to `maxPinterestImages` (5000)
pictures or `maxPinterestDownloadBytes` (2 GB), and "check daily for new
pictures" puts that sweep on a `webSyncEvery` (24 hour) schedule. Aiming it at
everyone rather than at one service made it broader, not safer. The desktop
keeps all of it, because it is not distributed by a store.

WHAT GOES, on the phone build only:

- The whole web importer: its panel, its four routes, its daily sync job, and
  its rows in the Plugins screen.
- The menu entry and the empty-library button that open it.

WHAT STAYS, and must keep working:

- **Paste a picture's link** (`#pasteURLForm` in the Add drawer). It posts to
  `/api/app/import-url`, a different route that fetches ONE picture the user
  pasted. That is what a browser does with "save image", not a scrape, and it
  is the thing that keeps "get a picture off the web" alive on the phone.
- The share sheet and the system photo picker.
- LAN sync from a desktop, which still has the whole importer.

PLAN:

1. `offersWebImport` build constant, true on desktop, false on mobile, next to
   `offersPinterest`. DONE.
2. `pluginEnabled("web")` and `setPluginEnabled("web")` refuse it when the
   build has none, so the config file cannot switch it back on. DONE.
3. The four `/api/app/plugins/web/*` and `/api/app/settings/web` routes are
   only registered when the build has the importer. DONE. `/api/app/import-url`
   is deliberately NOT in that group.
4. `freeOnPhone` loses `"web"`: nothing to unlock when it is compiled out.
   DONE.
5. Strip the importer's markup from the phone page, the way `withoutPinterest`
   already strips the board panel. DONE, as `withoutWebImport` next to it.
6. Give the phone's empty library a button that does something. Right now it is
   "Import from a link", which is the feature being removed, and it is already
   dead on arrival: `startPinterestOnboarding()` tries to switch the plugin on,
   `setPluginEnabled` now refuses, and the function returns having done
   nothing. DONE.

   Tiago's framing on 2026-09-08 says which button it should be: "Android
   doesn't need to understand Pinterest at all. Receives/syncs an existing
   Pictogrep library. Can also handle individual images through Android's Share
   menu." So the phone's first-run story is pair a computer, not add one
   picture. Primary button: Connect to computer (`#showSyncPhone`). The Add
   drawer, with the photo picker and paste-a-link, stays one tap away in the
   menu.
7. Tests, and the STORE.md Premium copy, which sold "import from web" as one of
   the two free phone features. DONE. `TestTheAndroidPageHasNoLinkImporter` is
   the new one; `TestPhoneFeatureGateFollowsTheSameUnlock` stopped asking
   whether a compiled-out feature is locked. PRIVACY.md also said the app could
   be pointed at a page, which is now false, so it was corrected too.

VERIFIED: arm64 app build green, core still position independent on the
/system loader. Desktop suite at its pre-existing 10 failures, android-tagged
suite at its pre-existing 26, no new ones in either. Compared against HEAD
rather than assumed.

NOT VERIFIED ON HARDWARE. Nobody has seen the new empty-library button on a
phone. It is one line of markup and one binding, but it is the first screen a
new user sees, so it belongs in the on-device pass in Android task 2.

### 38. Make all plugins free on desktop, mobile keeps paid plugins

Requested 2026-09-08. Then mid-turn: "make all plugins be like on every
install", which reads as the same ask restated, not a reversal: the desktop
free-for-all should hold everywhere the app is already installed, not just
on some fresh/future copy.

Found first: `pluginLocked` (`license.go:203`) is `manifest.Paid &&
!a.pluginsUnlocked()` with no platform check at all today, so a paid
installed plugin is gated identically on desktop and mobile. Separately,
plugins are not bundled into the app: `pluginsDir` starts empty on every
install, and the six plugins in the private `pictogrep-plugins-paid` repo
have no store/download/import flow built yet (`README.md`, "Selling and
packaging"), so nothing is actually gated in practice yet either way.

Asked Tiago which of two things this means: (a) just split the lock by
platform, so a plugin that does get installed is free on desktop and still
needs the mobile license, or (b) also build a bundling step that ships all
six plugin folders inside every desktop and Android build. He asked back
which is more reasonable.

DECIDED: (a). Reasons: it is the literal, smallest change that satisfies
both messages ("free on desktop", and it holds on every install because
`pluginLocked` is recomputed live on every request rather than a stored
flag, so nothing needs re-running per machine); it does not invent a
distribution mechanism nobody asked for; and bundling now would ship three
half-built skeletons (`darkroom`, `soundtrack`, and `storyboard` mid
extraction) as visible, broken panels. Plugins still reach `pluginsDir` the
same way they do today; only what happens once one is there changes.

Confirmed by Tiago: "leave phone (android) unchanged, plugins are paid on
android." Matches the plan below exactly.

PLAN:
- `license.go`: `pluginLocked` gains the same `!runsOnPhone ||` short-circuit
  `lockedOnPhone` already uses, so a `Paid` manifest is never locked on
  desktop and the existing check stands untouched on mobile.
- Update the comment on `pluginLocked` and the doc-comment block at the top
  of the file (currently "one license unlocks every paid plugin" with no
  platform mentioned) to say plugins are desktop-free and mobile-gated.
- `docs/plugins.md`'s Licensing section says NavyLilyWorks "unlocks every
  plugin" on desktop; correct that to say desktop plugins need no unlock at
  all, only mobile does.
- Check `plugins_test.go` / `license_test.go` / `plugin_capabilities_test.go`
  for existing assertions that a paid manifest is locked without a license,
  and add the desktop-vs-mobile split as its own case.
- `pictogrep-plugins-paid/README.md`'s "Selling and packaging" section
  describes a desktop buyer downloading a paid `.pictogrep` ZIP; flag it as
  stale rather than rewrite the pricing narrative, since that repo's business
  copy is not this task's call to make.

DONE, commit `0661041`. `pluginLocked` returns `false` outright when
`!runsOnPhone`, before ever looking at `manifest.Paid`; `lockedOnPhone` was
already shaped this way for the compile-time features, this just brings the
installed-plugin gate in line with it. Comments in `license.go` and
`docs/plugins.md`'s Licensing section updated to say the license machinery is
mobile-only now. `pictogrep-plugins-paid/README.md` flagged as stale, not
rewritten.

`TestPaidPluginIsUnreachableUntilLicensed` and
`TestPaidGateDoesNotSpecialCaseFirstPartyPlugins` now branch on `runsOnPhone`
the same way `TestPhoneFeatureGateFollowsTheSameUnlock` already did: desktop
asserts a `Paid` manifest is reachable and unlocked with no license at all,
the phone branch keeps the original assertions unchanged.

VERIFIED: `go test ./...` at the pre-existing 10 failures (all Pinterest),
`go test -tags pictogrep_android ./...` at the pre-existing 26, neither list
touching a license/plugin test. Both paid-gate tests pass under both tags.

Task 40, done the same session: tagged `v0.11.9` on this commit, pushed, CI
(`release.yml`) building and publishing Linux + Windows, per Tiago's explicit
call to keep using the existing tag-triggered workflow rather than block on
building a local-only release script that does not exist yet (see "Traps"
below). Changelog entry added.

Task 41, done the same session: local `~/.local/bin/pictogrep` rebuilt from
this working tree (`CGO_ENABLED=0`, `-ldflags "-s -w -X main.version=0.11.9"`,
matching the flake's own flags with 0.11.9 in place of the flake's stale
0.11.7), old binary backed up to the session scratchpad, process restarted
against the real library on :8765. `pictogrep version` reports `0.11.9`,
`/api/app/state` answers normally.

## Not doing

### 16. Autoscroll model, WITHDRAWN

Tiago floated the Windows middle-mouse autoscroll model (anchor point, delta
gives direction and speed, release stops) then withdrew it the same day:
"normal scroll is ok, ignore my previous message". The 1:1 drag panning with
momentum from tasks 14 and 15 stays as it is. Not implemented, do not revive
without asking.

### 18. Fling from the fastest part of the drag, DROPPED

The "throw using the quickest the drag ever got" model was dropped rather than
debugged: 18b replaced it with the ordinary kinetic scroll, which has no peak to
track. The measurements that looked flaky were partly the harness. The test
browser was being served the assets embedded in an OLD binary, so every result
before the rebuild described code that was not running. Rebuild the binary, do
not just edit `web/`.

The debug instrumentation the old entry warned about is gone.

## Done

### 1. Calendar dates were nonsense (root cause of "takes ages to load")

`fileMtime` returns `UnixNano`, but `calendarView` read it as Unix **seconds**:
`time.Unix(modified, 0)`. Every picture therefore landed in one bogus month
("February 56667832247"), so the calendar was a single section holding the
entire library, and every one of those pictures got a `stat` + open + image
decode on the way out. "Today" and "Yesterday" never matched either, and
`?month=YYYY-MM` never matched anything.

Fixed to `time.Unix(0, modified)` in `server.go`. Verified: 180 test pictures
now split into Today / Yesterday / 10 months.

### 2. Months sorted alphabetically

Sections were ordered by `Label > Label`, a string compare on "January 2006",
so October sorted before September. Now sorted on a new `Month` field
(`YYYY-MM`) with Today/Yesterday ranked ahead of it.

### 3. Older calendar sections load on demand

The server now only decodes pictures for the newest 3 sections
(`calendarEagerGroups`); older ones send label + count + month only. The client
renders those at their true height as skeletons and fills them in via
`IntersectionObserver` (600px lead) as they come into view. `browserImages` is
rebuilt from DOM order so the viewer's next/prev still matches reading order.
A failed section gets a "Try again" button.

Verified in Firefox: sections filled in correctly on scroll, API responds in
~1.5ms.

### 4. Calendar heading restyled

Sticky month heading (month at 20px/600, count right-aligned at 12px/600),
theme line below it at 13px. No gray ink anywhere: hierarchy is weight and
size only, per the no-gray-text rule. Section spacing moved to 32px, on the
`docs/ui.md` scale (was 40px, off-scale).

### 5. Header no longer sits on screen

Decision: the header becomes a normal element at the top of the page, scrolls
away, and only returns when you scroll back to the top.

- `.app-header` is no longer `position: fixed`.
- Removed the whole hide-on-scroll mechanism: `watchHeaderOnScroll()`,
  `body.header-hidden`, and the two `classList.remove("header-hidden")` calls.
- `.main-content` no longer reserves the header's height as top padding.
- `.plugin-sidebar` now spans from `top: 0`, and the `body.sidebar-open`
  negative-margin hack on the header is gone (a static header just shifts).

### 6. Folder title when a folder is open

`#folderTitle` in `web/index.html`, rendered by `renderFolderTitle()`. Shows the
folder name at 24px/600 with its picture count and an "All pictures" way out
beside it, sitting below the tabs and above the grid. Hooked into
`renderSearchScope()` (name) and `renderImages()` (count). New `folder.leave`
string added to both locales.

### 7. Tabs carry a minimal count, LATER REVERSED BY 13

Each tab appended a `.tab-count` span: Pictures showed the library count,
Folders how many folders. Undone by task 13.

### 8. Drag-to-scroll with a pen or mouse

`watchDragToScroll()` at the end of `web/app.js`. Hold the primary button on
empty space and drag to pan. Pointer Events, so mouse and pen are handled
together; touch is excluded so native scrolling is untouched. 4px threshold
before a press becomes a pan, and the trailing click is swallowed so a drag
never opens what it ended on. Interactive controls, `[draggable]`, picture and
folder cards, the canvas and its images are all refused at pointerdown, so
dragging pictures into folders still works. (Both of those last two were later
changed: see 14 and 23.)

Panning targets the nearest ancestor that actually scrolls (`scrollHeight >
clientHeight` and an `auto`/`scroll` overflow), so it works inside the drawer,
the plugin sidebar, the cover picker and related images, falling back to the
page. Horizontal panning included.

### 9. Verified in a real browser

Firefox + geckodriver + selenium against a generated 180-picture library, at
1440px and 390px. Confirmed: tabs read "Pictures 180 / Calendar / Folders 1";
the header scrolls off (bottom at -528px after a 600px scroll); the calendar
panel is visible in 36-85ms; month headings pin at top 0 and release with their
section; drag panning moved the page 1400 -> 1600 and cleaned up its class;
folder title reads "lib | 180 pictures | All pictures"; no JS errors.
`go test ./...` passes.

### 10. Tab switching is instant

Confirmed, not just assumed: `#calendarPanel` is already visible on the same
tick as the click (36ms at 390px, 85ms at 1440px). `switchTab()` runs
synchronously before any `await`, so the old delay was the main thread frozen
building a card for every picture in the library, which was task 1's bug. Fixed
by that plus the deferred sections.

### 11. Local install updated

Built from the working tree with the flake's ldflags, swapped into
`~/.local/bin/pictogrep` by atomic rename (the old build was running, so
overwriting in place was not safe). Backup of the previous binary kept in the
session scratchpad. Version deliberately left at 0.11.6: bumping is a release
action, a Go test pins `app.go` to `flake.nix`, and matching the published
version stops auto-update from clobbering the local build. Restarted the app on
port 8765 against the real 3419-picture library and opened it in Firefox.

### 12. "Full" edge-to-edge option under "Tamanho da imagem"

Requested 2026-09-06. The image size setting had 3 options; a "full" choice
below them makes the grid go edge to edge, ignoring the page width cap.
See task 29 for making it the default.

### 13. Remove the tab counts

Requested 2026-09-06, reversing task 7. Tiago does not want numbers on the
Pictures / Folders tabs at all. Stripped `renderTabCounts`, `setTabCount`,
`formatCount`, the `.tab-count` CSS and the changelog line about it.

### 14. Drag-to-scroll works on images too

Requested 2026-09-06. Panning works almost everywhere, including on top of
pictures: dragging a picture scrolls the page rather than dragging the picture.
Still excluded: tabs, buttons and other controls. DECIDED 2026-09-06: pictures
always scroll and drag-into-folder is dropped entirely. Filing happens through
the right-click menu and the "Add to folder" actions instead. `draggable` /
`ondragstart` on picture cards and the drop targets that fed them are gone.

### 15. Momentum on the drag

Releasing a fast drag throws the page and the speed decays. Velocity is read
from the last 90ms of the drag, not the final event, because one event is
mostly jitter. Decay is applied per elapsed second, not per frame, so the throw
covers the same distance at 60Hz and 144Hz. Pressing down, a wheel or a key
stops it dead. Skipped entirely under `prefers-reduced-motion`.

Verified with synthetic pointer events at real timings (geckodriver cannot
produce a flick, it interpolates every move into 6px steps ~18ms apart):
hand at ~5000px/s coasted 804px, ~1875px/s coasted 302px, ~250px/s coasted 0px
(under the fling floor, so a deliberate placement does not drift).

### 17. Faster panning, SUPERSEDED by 18b

"way faster". The page was geared 2.2x the hand, the fling speed cap went from
9k to 20k px/s, and friction dropped so a throw carried roughly its release
speed / 2.4. Measured: hand at ~5000px/s coasted 4464px (was 804), ~1875px/s
1662px, ~250px/s 210px. All of it replaced by 18b's 1:1 touchpad model.

### 18b. Scroll like a touchpad, SUPERSEDES 18, 17 and 19

Requested 2026-09-06: "just make it be like the same UX of scrolling with a
touchpad or mouse scroll wheel, after you leave the motion it still does like a
bit more like a car stopping". So the model is the ordinary kinetic scroll
everyone already knows: the throw comes from how fast the hand was going when it
let go, and it eases down like a car braking rather than stopping dead. The
"fastest stretch of the drag" idea from 18 is dropped with it, which also
removes the flakiness measured there.

Tiago also asked for one shared ending: "you don't need a separate scrolling
implementation, unify the final operation", drawn as every input funnelling into
`scroll(dx, dy)`. So the drag and the glide it throws now both call one
`scroll()` inside `watchDragToScroll()`, which owns the target container, the
whole-pixel carry, and `target.scrollBy({left, top, behavior: "auto"})`, the
same call and the same container the wheel and the touchpad already move.

Then: "make it work like a touchpad scrolling". The 2.2x gearing from task 17 is
gone; the page follows the hand exactly 1:1 and the distance comes from the
throw. The fling floor dropped to 300px/s to match the un-geared speeds.
(Gain later raised to 1.15 by task 25, then 1.5 by task 26.)

Measured against a rebuilt binary, 180-picture library, Firefox 1440x900:

    hand ~5000px/s   drag 480px, coast 2412px
    hand ~1650px/s   drag 240px, coast  837px
    hand  ~120px/s   drag  36px, coast    0px  (a placement does not drift)
    fast, 200ms pause before release   coast 0px
    fast, 600ms pause before release   coast 0px

The pause cases are what task 18 could never make behave. Velocity is now read
from the last 100ms measured back from the RELEASE, not from the last move, so a
hand that stopped has an empty window and the page stays where it was put.

### 19. Make it smooth, DONE with 18b

"just make it super super intuitive and SMOOTH". Fractional scroll remainders
are carried between calls instead of being lost to whole-pixel rounding, which
otherwise reads as a stutter through the whole glide, worst where it is
slowest. That carry lives in the single `scroll()`, so the drag gets it too.

### 23. Forgive a shaky click

Requested 2026-09-06: "put a bit of a forgiveness so you can click on images
even if you dragged a little bit, cause pens are a bit more unstable than a
mouse, they don't have a resting point often". A pen wobbles while it is being
pressed, so a click that moved a few pixels is still a click. Then: "even if
youre dragging a bit, it counts as a click, only BIG drags are scrolls".

Two thresholds now. `SLOP` (24px from where the press landed) is how far the pen
has to travel before it is panning at all, and the pan starts from there rather
than jumping the page by the whole slop. `FORGIVE` (40px) is checked at the
release: a gesture that ended within it still opens what was pressed and does
not throw the page, so a wobble that crossed the slop and came back is still a
click.

Measured, pen events on a picture card: 6px and 18px wobble open the picture and
move the page 0px; 30px opens it and moves 5px; 60px and 200px scroll (313px and
1140px with the throw) and do not open anything.

### 24. Drag-scrolling on top of an image was broken

Reported 2026-09-06: "fix dragscrolling when youre dragging on images, its kinda
broken". An `<img>` is draggable by default, so pressing a picture and moving
started the browser's own image drag and the pan never happened. `loadImage()`
now sets `image.draggable = false`, which is the one place every picture in the
grid goes through. Verified: a press that starts on a picture scrolls the page.
The viewer's own `<img>` is NOT built there: that is task 30.

### 25. Scroll 15% faster

Requested 2026-09-06, on top of the 1:1 touchpad feel from 18b: the page moves
1.15x the hand, so a drag covers a bit more ground without losing the sense that
the content is following the pen.

### 26. Faster and simpler still

Requested 2026-09-06 right after the release: "faster, simpler". Gain 1.15 ->
1.5, and the speed cap dropped: it was sized for the old 2.2x gearing and a hand
can no longer reach it, so it was a constant that did nothing.

### 31. Reordering folders on the Folders tab was broken

Reported 2026-09-08: "fix reordering folder orders on folders tab on pictogrep
desktop".

None of the suspects were it, and neither was drag-to-scroll: `.folder-card` is
in its ignore list. The drag starts, the reorder maths is right and the order
reaches disk. Measured against a rebuilt binary, dropping `lib` after `bravo`
POSTed exactly `alpha, bravo, lib, charlie, delta, echo` and the server stored
it. Then the screen showed `lib, alpha, bravo, charlie, delta, echo` anyway.

`renderFolders()` sorted by picture count and never read `folderView.order`, so
every reorder was thrown away one line after being saved. It called neither the
`folderComparator()` next to it, which did handle a custom order and was dead
code, nor anything else that looked at the saved order.

`folderOrdering()` replaces that comparator and is used by both the render and
the drop. Hand-placed folders keep the position they were given; anything made
since the last reorder is ranked by size after them, so the approved
biggest-first wall is still what a library that has never been reordered gets,
and a new folder does not jump to the front.

Fixed a second bug in the same feature while the order was inert and could not
show it: `handleFolderDrop()` rebuilt the order from the visible cards and
appended the rest, so reordering with the folder search box filtering sent every
folder that did not match to the end. It now moves the dragged folder inside the
full order.

Verified in Firefox at 1440px and 390px, live and across a reload: a drop
sticks, a drop under a filter leaves the non-matching folders where they were,
and a folder created afterwards lands last. `go test ./...` has 11 failures,
all Pinterest and import tests, byte-identical to the same 11 on HEAD.

The drop side is still chosen by the card's horizontal midpoint. Checked
whether that reads wrong on a phone: it does not, the wall is two columns at
390px, so cards sit side by side and left/right is the right axis. Left alone.

### 35. Two blank buttons in the Android build

Found 2026-09-08 while auditing what was left before the Play release. Not
reported by anyone: it was found by dumping the page the app build actually
serves, rather than by reading `index.html`.

`withoutPinterest()` in `server.go` empties the board importer out of the phone
page by id. Three of those ids outlived what they were named after. When the
board panel merged into the general link importer, `#showPinterest` became the
menu's "Import from a link" and `#emptyPinterest` / `#emptyPinterestPhone`
became the same offer on an empty library. All three still work in the app
build, and all three were being hollowed by name, so the phone shipped a blank
row in the menu and a blank primary button on the empty-library screen, which
is the first screen a new user sees. "Broken functionality" is a Play rejection
reason on its own.

Fixed by cutting those three from `pinterestParts`, plus `#pinterestSection`,
which no longer exists at all and had been a silent no-op. What is still
hollowed is only what is genuinely board-specific: the two settings rows and
the followed-boards list.

`TestTheAndroidPageHasNoBoardImporter` was asserting on ids that had been gone
for weeks, so it was failing rather than catching this, and was one of the 11
pre-existing failures. It now checks the current ids and, in the other
direction, that both link-importer buttons still carry their label. Suite is
down to 10 failures, all pre-existing desktop import tests.

### 32. The open-a-folder animation was hideous

Requested 2026-09-08: "also fix the animation for when you open a folder, its
hideous".

What was ugly was the shape, not the timing. `openFolder()` gave the folder card
and `#imagesPanel` the SAME `view-transition-name`, which is how you ask the
browser to pair them, so one rectangle travelled from a 265x254 card to a
1120px-wide panel while both snapshots were force-fitted into it (`height: 100%`
plus `object-fit: cover`). The card was blown up about 5x and cropped, the grid
was squeezed into a card and blown back out, and the two cross-dissolved through
the middle of it. An earlier pass had already tried to rescue this by swapping
`fill` for `cover`, which only changed distortion into a cropped smear.

The morph is gone. Each panel has its own name now, `folder-wall` and
`folder-contents`, so neither is ever paired, stretched or cropped: one leaves,
one arrives, each in its own box. The wall dissolves, the grid fades in and
rises 10px into place on the app's own `cubic-bezier(.2, 0, 0, 1)`. Both fades
are linear on purpose, because easing the two halves of a dissolve stops them
adding up to one and dims the middle of the swap. Duration is .2s, down from
.26s, which puts it near the .12s tab underline instead of at twice its length.

The transition is now gated on the folders wall being on screen rather than on a
card being passed, so the context menu's Open animates the same as clicking a
card, and the path that opens a folder after a web import stays instant as it
was. `openFolder()` lost its unused `card` argument.

Verified in Firefox: `::view-transition-old(folder-wall)` runs `folder-swap-out`
200ms linear, `::view-transition-new(folder-contents)` runs `folder-swap-in`
200ms linear plus `folder-arrive` 200ms `cubic-bezier(0.2, 0, 0, 1)`, there is
no paired group and no `::view-transition-old(folder-contents)`, and the panel
names are cleaned off after it finishes.

## Traps worth remembering

- **Rebuild the binary, do not just edit `web/`.** The assets are embedded, so a
  test browser served by a stale binary is testing code that is not running.
  This silently invalidated every measurement in task 18.
- **geckodriver screenshots do NOT capture view-transition snapshot layers.**
  They photograph the live DOM underneath, so a paused transition screenshots as
  the finished state. Read `getAnimations()` and the pseudo-element names
  instead of trying to eyeball frames (task 32).
- **geckodriver cannot produce a flick.** It interpolates every move into 6px
  steps ~18ms apart, so momentum has to be tested with synthetic pointer events
  at real timings (task 15).
- **Bound every string search when splicing code.** An unbounded `str.index` for
  the end of a range matched an EARLIER `const finish = () => {` at line 120 and
  duplicated ~5800 lines of `web/app.js`. Recovered by reconstruction. Always
  `s.index(needle, start)`.
- `go test ./...` has 10 pre-existing failures on HEAD, all Pinterest and import
  tests; `go test -tags pictogrep_android ./...` has 26. Compare against HEAD
  before blaming your own change, and check both tag sets for anything that
  branches on `runsOnPhone`.
- **The source `version` constant (`app.go`, `flake.nix`) does not track the
  latest tag.** `v0.11.8`'s own tree still says `"0.11.7"` in both places; the
  release workflow gets its version from the git tag via `-X main.version=`,
  not from source. Bumping that constant is a separate, occasional action of
  Tiago's, not something a release needs. A local build that wants to report
  the version it actually corresponds to should pass `-ldflags "-X
  main.version=..."` directly rather than trust `nix build`'s flake version
  (task 38's local install did this to get `0.11.9` instead of the stale
  `0.11.7` the flake would have baked in).

## State

Committed and pushed to `main` (`0661041`), tagged `v0.11.9`, CI building and
publishing the release. Local `~/.local/bin/pictogrep` rebuilt from this same
commit and running, reporting `0.11.9`.

## Standing rules

- Every task Tiago gives goes into this file BEFORE work starts on it. Always.
- Never gray text. Differentiate with weight, size, or the accent colour.
- No em dashes anywhere.
- Build and check every UI change phone-first (~390px), then desktop.
- Home page and folders page designs are approved; do not restyle them as a
  side effect. `docs/ui.md` is the blueprint.
- Pictogrep releases are built and published locally, never via CI.
- Ask before pushing anything to the public pictogrep repo.

## Plugin CSP: font-src data: (2026-09-15)
- [x] servePlugin allows `font-src data:` so a plugin can inline its own
      typeface. A plugin file has no CORS header and @font-face always fetches
      in cors mode, so an inlined font is the only one a sandboxed plugin can
      use. threedraw inlines MEK Mono this way. Covered in plugins_test.go.
