# Session tasks

Started 2026-09-06. Work on the calendar view, the app header, and the folder page.

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

### 7. Tabs carry a minimal count

Each tab appends a `.tab-count` span: Pictures shows the library count, Folders
shows how many folders. Nothing else, and the count inherits the tab's own
colour so it dims and lights with the tab. Rounds to "1.2k" past a thousand so
it cannot outgrow the tab. Rendered by `renderTabCounts()` from `renderState()`
and `loadFolders()`.

### 8. Drag-to-scroll with a pen or mouse

`watchDragToScroll()` at the end of `web/app.js`. Hold the primary button on
empty space and drag to pan. Pointer Events, so mouse and pen are handled
together; touch is excluded so native scrolling is untouched. 4px threshold
before a press becomes a pan, and the trailing click is swallowed so a drag
never opens what it ended on. Interactive controls, `[draggable]`, picture and
folder cards, the canvas and its images are all refused at pointerdown, so
dragging pictures into folders still works.

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

## Pending

### 12. DONE. "Full" edge-to-edge option under "Tamanho da imagem"

Requested 2026-09-06. The image size setting has 3 options; add a "full" choice
below them that makes the grid go edge to edge (ignoring the page width cap).

### 13. DONE. Remove the tab counts

Requested 2026-09-06, reversing task 7. Tiago does not want numbers on the
Pictures / Folders tabs at all. Strip `renderTabCounts`, `setTabCount`,
`formatCount`, the `.tab-count` CSS and the changelog line about it.

### 14. DONE. Drag-to-scroll should work on images too

Requested 2026-09-06. Panning should work almost everywhere, including on top
of pictures: dragging a picture should scroll the page, not drag the picture.
Still excluded: tabs, buttons and other controls. DECIDED 2026-09-06: pictures
always scroll and drag-into-folder is dropped entirely. Filing happens through
the right-click menu and the "Add to folder" actions instead. Remove
`draggable`/`ondragstart` on picture cards and the drop targets that fed it.

### 15. Momentum on the drag, DONE

Releasing a fast drag throws the page and the speed decays. Velocity is read
from the last 90ms of the drag, not the final event, because one event is
mostly jitter. Decay is applied per elapsed second, not per frame, so the throw
covers the same distance at 60Hz and 144Hz. Pressing down, a wheel or a key
stops it dead. Skipped entirely under `prefers-reduced-motion`.

Verified with synthetic pointer events at real timings (geckodriver cannot
produce a flick, it interpolates every move into 6px steps ~18ms apart):
hand at ~5000px/s coasted 804px, ~1875px/s coasted 302px, ~250px/s coasted 0px
(under the fling floor, so a deliberate placement does not drift).

### 16. Autoscroll model, WITHDRAWN

Tiago floated the Windows middle-mouse autoscroll model (anchor point, delta
gives direction and speed, release stops) then withdrew it the same day:
"normal scroll is ok, ignore my previous message". The 1:1 drag panning with
momentum from tasks 14 and 15 stays as it is. Not implemented, do not revive
without asking.

### 17. Faster panning, DONE then SUPERSEDED by 18b

"way faster". The page is now geared 2.2x the hand, the fling speed cap went
from 9k to 20k px/s, and friction dropped so a throw carries roughly its release
speed / 2.4. Measured: hand at ~5000px/s coasts 4464px (was 804), ~1875px/s
coasts 1662px, ~250px/s coasts 210px.

### 18. Fling from the fastest part of the drag, DROPPED

The "throw using the quickest the drag ever got" model was dropped rather than
debugged: 18b replaced it with the ordinary kinetic scroll, which has no peak to
track. The measurements that looked flaky were partly the harness. The test
browser was being served the assets embedded in an OLD binary, so every result
before the rebuild described code that was not running. Rebuild the binary, do
not just edit `web/`.

The debug instrumentation the old entry warned about is gone.

### 18b. Scroll like a touchpad, DONE, SUPERSEDES 18, 17 and 19

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
gone; the page now follows the hand exactly 1:1 and the distance comes from the
throw. The fling floor dropped to 300px/s to match the un-geared speeds.

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
slowest. That carry now lives in the single `scroll()`, so the drag gets it too.

### 23. Forgive a shaky click, DONE

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

### 24. Drag-scrolling on top of an image is broken, DONE

Reported 2026-09-06: "fix dragscrolling when youre dragging on images, its kinda
broken". Dragging over a picture is supposed to scroll exactly like dragging
over empty space. Reproduce first and say what actually differs: likely
suspects are the browser's own image drag starting instead, `.card-menu` or a
card overlay swallowing the press, or the picture's own click handler firing at
the end of the drag.

It was the first one. An `<img>` is draggable by default, so pressing a picture
and moving started the browser's own image drag and the pan never happened.
`loadImage()` now sets `image.draggable = false`, which is the one place every
picture in the app goes through. Verified: a press that starts on a picture
scrolls the page.

### 25. Scroll 15% faster, DONE

Requested 2026-09-06, on top of the 1:1 touchpad feel from 18b: the page moves
1.15x the hand, so a drag covers a bit more ground without losing the sense
that the content is following the pen.

### 26. Faster and simpler still

Requested 2026-09-06 right after the release: "faster, simpler". Gain 1.15 ->
1.5, and the speed cap dropped: it was sized for the old 2.2x gearing and a hand
can no longer reach it, so it was a constant that did nothing.

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
entirely inside `#drawer` and `#pluginSidebar`. That panel is packed with
small controls close together (radio cards, checkboxes, labels wrapping their
own input), so a drag that starts a few pixels off one of them could still be
read as meant for it; the panel already scrolls fine on its own, so panning
there was not worth the risk. NEEDS TIAGO to confirm the Pinterest screen
works now.

### 28. Remove Pinterest branding, accept any link

Requested 2026-09-06. Same task filed in `pictogrep-android/TODO.md` for the
Android side. On desktop:

- Drop the Pinterest name, icon and red accent (`#bd081c`) from the import UI:
  `#showPinterest`, `#pinterestSection`, the `.pinterest-*` classes in
  `web/app.css`, and the `pinterest.*` strings in both locale files. Reframe as
  a general "import from a link" flow.
- `importPinterestBoard()` in `web/app.js` currently rejects anything whose
  hostname does not match `pinterest.\.` and needs at least 2 path segments.
  Accept any URL; `web_source.go` / `native_gallery.go` already have a
  general web importer to compare against and possibly fold into.
- Check `pinterest.go`, `pinterest_sync.go`, `native_gallery_pinterest.go`,
  `native_gallery_pinterest_mobile.go` for anything Pinterest-specific in the
  backend that would need to generalize too, not just the front end.

DECIDE with Tiago whether this replaces the existing "Import from a web page"
flow (`webSection`/`web.*`) entirely, since they would do the same job.

### 29. Make "Full width" the default for new users

Requested 2026-09-06 ("total width largura total be the default option for new
users"). The Full width grid setting from task 12 defaults to off; new users
should start with it on. Existing users' saved choice must not change under
them.

### 30. Dragging is still native inside an open picture

Reported 2026-09-06: "also that still can drag on images after you open them".
Task 24 made `loadImage()` set `image.draggable = false`, which covers every
picture in the grid, but the full-size image inside `#imageViewer` is not
created there. Find where the viewer builds its `<img>` and fix the same way.

### 20. Drawing drops areas inside images

Reported 2026-09-06: "drawing works not well like inside images, areas
missing". First guess was that drag-to-scroll was stealing the strokes. That is
WRONG: drawing lives in `web/practice.html`, which loads neither `app.js` nor
`app.css` and has its own pointer handling (`setPointerCapture`,
`getCoalescedEvents`). So this is pre-existing and unrelated to this session's
work. Still needs investigating on its own, and needs Tiago to say which tool
and what "areas missing" looks like.

### 21. REGRESSION: cannot click the Pinterest import text box

Reported 2026-09-06: "importing from pinterest stopped working, cannot click on
text box". Prime suspect is this session's drag-to-scroll, most likely the
capture-phase click swallow after a pan, or a stuck `body.page-panning` class
leaving `user-select: none` behind. NOT REPRODUCED 2026-09-06. In Firefox
against a fresh build, the board link box takes a real mouse click and typed
text, with focus landing on `#pinterestBoardURL`; a text box clicked straight
after a pan also focuses and types, `body.page-panning` is not left behind and
`body` holds no pointer capture. The likeliest cause was task 22's
`setPointerCapture` on `document.body`, which retargets pointer events while a
drag is live, and that is now gone. NEEDS TIAGO to try it again and say whether
it is still broken.

### 22. Turn off "Site has control of your pointer"

Reported 2026-09-06. Firefox shows this for the Pointer Lock API, but nothing
in `web/` calls `requestPointerLock`. The only pointer API in play is
`setPointerCapture`: mine on `document.body` in the pan (removable, the
document-level listeners already cover it) and pre-existing ones in
`practice.html` (NOT removable, strokes need to continue outside the canvas).
DONE: mine is removed. The drag is carried by the document-level listeners
alone, and leaving the window simply ends it, which is what letting go means
anyway. Verified `document.body.hasPointerCapture(1)` is false after a pan.
If Firefox still shows the message, it is `practice.html`, so ask Tiago where
exactly he sees it.

## Notes

A splice using an unbounded `str.index` for the end of a range matched an
EARLIER `const finish = () => {` at line 120 and duplicated ~5800 lines of
`web/app.js`. Recovered by reconstruction. Always bound the search:
`s.index(needle, start)`.

Working tree is still uncommitted and nothing has been pushed.


## Working rules Tiago set this session

- **Every task he gives goes into this file BEFORE work starts on it.** Always.

## Reminders that apply to this work

- Never gray text. Differentiate with weight, size, or the accent colour.
- No em dashes anywhere.
- Build and check every UI change phone-first (~390px), then desktop.
- Home page and folders page designs are approved; do not restyle them as a
  side effect. `docs/ui.md` is the blueprint.
- Pictogrep releases are built and published locally, never via CI.
- Ask before pushing anything to the public pictogrep repo.
