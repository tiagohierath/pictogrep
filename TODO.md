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
- `go test ./...` has 11 pre-existing failures on HEAD, all Pinterest and import
  tests. Compare against HEAD before blaming your own change.

## State

Working tree is uncommitted and nothing has been pushed. Released 0.11.7 during
this session; the local `~/.local/bin/pictogrep` build is ahead of it.

## Standing rules

- Every task Tiago gives goes into this file BEFORE work starts on it. Always.
- Never gray text. Differentiate with weight, size, or the accent colour.
- No em dashes anywhere.
- Build and check every UI change phone-first (~390px), then desktop.
- Home page and folders page designs are approved; do not restyle them as a
  side effect. `docs/ui.md` is the blueprint.
- Pictogrep releases are built and published locally, never via CI.
- Ask before pushing anything to the public pictogrep repo.
