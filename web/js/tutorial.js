/*
 * Copyright 2025-2026 Nuvolaris Inc
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU Affero General Public License as published
 * by the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU Affero General Public License for more details.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program.  If not, see <https://www.gnu.org/licenses/>.
 */

/*
 * Interactive guided tutorials (spec/16-tutorial.md).
 *
 * A tutorial is a spotlight walkthrough: a semi-transparent white overlay
 * covers the whole application, holes are cut around the controls the user may
 * act on, and a card next to them says what to do. The overlay itself is what
 * disables everything else — clicks land on a mask, never on the application
 * underneath — while the holes leave the real controls clickable, including
 * when they live inside the TruACP iframe.
 *
 * The hole is cut in the *viewport*, not in the target: the masks tile around
 * a set of rectangles and nothing about the target is touched. That is what
 * makes modals, arbitrary stacking contexts and the cross-origin iframe all
 * work without trying to raise an element out of a frame it cannot leave.
 *
 * Steps never advance on a click. They advance when the application state says
 * the action completed (the notebook panel really opened, the commit really
 * succeeded), which is polled on a timer so the tour follows the app rather
 * than replaying a fixed slideshow. Notebook state lives in the TruACP frame on
 * another origin and arrives through the tour bridge (trustable-acp
 * web/tour-bridge.ts).
 *
 * The engine's first duty is never to trap the user. Every step can be skipped
 * once it has waited too long, explanation buttons are never withheld because
 * something else is disabled, and a step whose target vanishes falls back to
 * spotlighting the modal it lives in so the error and its recovery buttons
 * stay reachable.
 *
 * Loaded by app.html (which also owns the Tutorial menu) and by applist.html:
 * both current tours run on app.html, but the engine supports a tutorial that
 * crosses pages and resumes from sessionStorage, so applist.html keeps it.
 */
(function () {
    'use strict';

    const STORAGE_KEY = 'trustable.tutorial';
    const TICK_MS = 150;
    // Frame reports arrive every 200ms; anything older means the frame went
    // away (navigation, reload, hidden panel) and its targets are unusable.
    const FRAME_STALE_MS = 1500;
    const HOLE_PAD = 6;
    // How long a `frame:` step waits before it stops blaming the user and names
    // the bridge instead: a silent bridge is a configuration problem the user
    // has to be told about, not something that resolves on its own. Skipping no
    // longer waits on a timeout — every step offers "Skip this step" outright.
    const FRAME_HELP_MS = 10000;
    // Protocol revision this engine speaks to the tour bridge.
    const FRAME_PROTOCOL = 2;

    const page = window.location.pathname.endsWith('applist.html') ? 'applist'
        : window.location.pathname.endsWith('app.html') ? 'app'
        : 'other';

    // ---------------------------------------------------------------- helpers

    function el(id) {
        return document.getElementById(id);
    }

    function cookie(name) {
        const match = document.cookie.match(new RegExp('(^| )' + name + '=([^;]+)'));
        return match ? decodeURIComponent(match[2]) : '';
    }

    function now() {
        return Date.now();
    }

    // ------------------------------------------------------------ frame bridge

    // Last report from the TruACP iframe: control rects in frame-viewport
    // coordinates plus the notebook state the steps wait on.
    const frame = { targets: {}, state: null, at: 0, version: 0 };

    function frameOrigin() {
        const left = cookie('LEFT');
        if (!left) return '';
        try {
            return new URL(left).origin;
        } catch (e) {
            return '';
        }
    }

    function frameWindow() {
        const iframe = el('leftFrame');
        return iframe && iframe.contentWindow ? iframe.contentWindow : null;
    }

    function tellFrame(message) {
        const win = frameWindow();
        const origin = frameOrigin();
        if (!win || !origin) return;
        win.postMessage(Object.assign({ source: 'trustable-tour-host' }, message), origin);
    }

    window.addEventListener('message', (event) => {
        const data = event.data;
        if (!data || data.source !== 'trustable-tour-frame' || data.type !== 'frame') return;
        // Fail closed. With no parsable LEFT cookie there is no origin to trust,
        // and the right-hand preview iframe runs user application code that
        // could otherwise steer the spotlight onto arbitrary coordinates.
        const origin = frameOrigin();
        if (!origin || event.origin !== origin) return;
        frame.targets = data.targets || {};
        frame.state = data.state || null;
        frame.version = data.version || 1;
        frame.at = now();
    });

    function frameFresh() {
        return now() - frame.at < FRAME_STALE_MS;
    }

    /**
     * Notebook state, or null when it is unknown. Unknown must never read as
     * "false": a stale frame used to make `panelOpen === true` and
     * `panelOpen === false` both permanently untrue, which is what hung the
     * Notebook tutorial forever.
     */
    function frameState() {
        return frameFresh() ? frame.state : null;
    }

    /** True only when the bridge is reporting and too old to drive the tour. */
    function frameOutdated() {
        return frameFresh() && frame.version < FRAME_PROTOCOL;
    }

    function frameIframe() {
        const iframe = el('leftFrame');
        if (!iframe || iframe.classList.contains('hidden')) return null;
        if (iframe.getClientRects().length === 0) return null;
        return iframe;
    }

    /**
     * Every reported match for a `data-tour` name, in frame coordinates. The
     * bridge reports all of them so a specific catalog entry can be picked;
     * revision 1 sent a bare object, which is normalised here to one match.
     */
    function frameMatches(name) {
        if (!frameFresh()) return [];
        const found = frame.targets[name];
        if (!found) return [];
        return Array.isArray(found) ? found : [found];
    }

    /**
     * Frame rects are relative to the iframe viewport; shift into ours and clip
     * to the iframe box. Without the clip a scrolled notebook list punches the
     * hole over the preview pane or the top bar, exposing the wrong control.
     */
    function frameRect(name, pick) {
        const iframe = frameIframe();
        if (!iframe) return null;
        const matches = frameMatches(name);
        if (!matches.length) return null;

        let rect = matches[0];
        if (typeof pick === 'number') {
            rect = matches[pick];
        } else if (typeof pick === 'string') {
            rect = matches.find((m) => (m.label || '') === pick) || null;
        }
        if (!rect) return null;

        const box = iframe.getBoundingClientRect();
        const left = box.left + rect.x;
        const top = box.top + rect.y;
        const clipped = intersect(
            { x: left, y: top, width: rect.width, height: rect.height },
            { x: box.left, y: box.top, width: box.width, height: box.height }
        );
        if (!clipped) return null;
        return {
            x: clipped.x,
            y: clipped.y,
            width: clipped.width,
            height: clipped.height,
            // A rect the frame clipped away is off its own fold: report it so
            // the step can ask the frame to scroll rather than wait forever.
            offscreen: clipped.width < rect.width - 1 || clipped.height < rect.height - 1,
            disabled: !!rect.disabled,
            frame: true,
            frameTarget: name,
            framePick: pick
        };
    }

    function intersect(a, b) {
        const x = Math.max(a.x, b.x);
        const y = Math.max(a.y, b.y);
        const right = Math.min(a.x + a.width, b.x + b.width);
        const bottom = Math.min(a.y + a.height, b.y + b.height);
        if (right <= x || bottom <= y) return null;
        return { x: x, y: y, width: right - x, height: bottom - y };
    }

    function hostRect(selector) {
        let node;
        try {
            node = document.querySelector(selector);
        } catch (e) {
            return null;
        }
        if (!node || node.getClientRects().length === 0) return null;
        const box = node.getBoundingClientRect();
        if (box.width <= 0 || box.height <= 0) return null;
        return {
            x: box.left,
            y: box.top,
            width: box.width,
            height: box.height,
            disabled: !!node.disabled,
            node: node
        };
    }

    /**
     * A target is a host selector, `frame:<name>`, or `frame:<name>@<pick>`
     * where pick is an index or a label. Functions receive the step context.
     */
    function resolveTarget(target, c) {
        if (typeof target === 'function') target = target(c);
        if (!target) return null;
        if (target.indexOf('frame:') !== 0) return hostRect(target);
        const rest = target.slice(6);
        const at = rest.indexOf('@');
        if (at < 0) return frameRect(rest, undefined);
        const pick = rest.slice(at + 1);
        const asIndex = Number(pick);
        return frameRect(rest.slice(0, at), Number.isInteger(asIndex) ? asIndex : pick);
    }

    /** Steps declare one target or several; always work with a list. */
    function resolveTargets(step, c) {
        const spec = step.targets || (step.target ? [step.target] : []);
        const holes = [];
        for (const one of spec) {
            const hole = resolveTarget(one, c);
            if (hole) holes.push(hole);
        }
        return holes;
    }

    // ------------------------------------------------------------------- state

    function load() {
        try {
            return JSON.parse(sessionStorage.getItem(STORAGE_KEY) || 'null');
        } catch (e) {
            return null;
        }
    }

    function save(state) {
        sessionStorage.setItem(STORAGE_KEY, JSON.stringify(state));
    }

    function clear() {
        sessionStorage.removeItem(STORAGE_KEY);
    }

    // ----------------------------------------------------------------- overlay

    let ui = null;
    let timer = null;
    let current = null;    // { tour, steps, index, data, holes, enteredAt }
    let scrolledFor = -1;  // step index a target was last scrolled into view for

    /** Built with DOM calls rather than one innerHTML string, so the overlay
     *  can be constructed under a test harness without an HTML parser. */
    function make(tag, className, parent) {
        const node = document.createElement(tag);
        if (className) node.className = className;
        if (parent) parent.appendChild(node);
        return node;
    }

    function buildOverlay() {
        if (ui) return ui;
        const root = document.createElement('div');
        root.id = 'tutorialOverlay';
        document.body.appendChild(root);

        const maskLayer = make('div', 'tutorial-masks', root);
        const ringLayer = make('div', 'tutorial-rings', root);
        const card = make('div', 'tutorial-card', root);
        card.setAttribute('role', 'dialog');
        card.setAttribute('aria-live', 'polite');
        const stepLabel = make('div', 'tutorial-card-step', card);
        const title = make('h4', 'tutorial-card-title', card);
        const text = make('p', 'tutorial-card-text', card);
        const wait = make('div', 'tutorial-card-wait', card);
        make('span', 'tutorial-spinner', wait);
        const waitText = make('span', 'tutorial-wait-text', wait);
        const actions = make('div', 'tutorial-card-actions', card);
        // Links rather than buttons, on one line at the foot of the card: both
        // are always available but must not compete with the step's own action.
        const links = make('div', 'tutorial-card-links', card);
        const skip = make('button', 'tutorial-link tutorial-skip', links);
        skip.type = 'button';
        skip.textContent = 'Skip this step';
        const exit = make('button', 'tutorial-link tutorial-exit', links);
        exit.type = 'button';
        exit.textContent = 'Exit tutorial';

        ui = {
            root: root,
            maskLayer: maskLayer,
            ringLayer: ringLayer,
            masks: [],
            rings: [],
            card: card,
            step: stepLabel,
            title: title,
            text: text,
            wait: wait,
            waitText: waitText,
            actions: actions,
            skip: skip
        };
        skip.addEventListener('click', () => {
            if (!current) return;
            current.error = null;
            advance();
        });
        exit.addEventListener('click', () => stop());
        // The masks are the disabling layer. Swallowing the press outright also
        // stops it reaching document-level handlers — the pages close their
        // Config/Utils/Tutorial dropdowns on any document click, so a click on
        // the mask used to dismiss menus behind the overlay.
        ['mousedown', 'mouseup', 'click', 'dblclick', 'contextmenu'].forEach((type) => {
            ui.maskLayer.addEventListener(type, (event) => {
                event.preventDefault();
                event.stopPropagation();
            }, true);
        });
        return ui;
    }

    /** Grow or shrink a pooled list of absolutely-placed divs. */
    function pool(list, layer, className, count) {
        while (list.length < count) {
            const node = document.createElement('div');
            node.className = className;
            layer.appendChild(node);
            list.push(node);
        }
        for (let i = count; i < list.length; i++) list[i].style.display = 'none';
        for (let i = 0; i < count; i++) list[i].style.display = '';
        return list;
    }

    function place(node, box) {
        node.style.left = Math.round(box.x) + 'px';
        node.style.top = Math.round(box.y) + 'px';
        node.style.width = Math.round(Math.max(0, box.width)) + 'px';
        node.style.height = Math.round(Math.max(0, box.height)) + 'px';
    }

    /** Pad a hole and clip it to the viewport; null when nothing is left. */
    function padded(hole, vw, vh) {
        return intersect(
            {
                x: hole.x - HOLE_PAD,
                y: hole.y - HOLE_PAD,
                width: hole.width + HOLE_PAD * 2,
                height: hole.height + HOLE_PAD * 2
            },
            { x: 0, y: 0, width: vw, height: vh }
        );
    }

    /**
     * Cover the viewport except for the holes. The viewport is cut into
     * horizontal bands at every hole edge, and within each band the spans not
     * covered by a hole become masks. One hole reduces to the familiar four
     * rectangles; several holes are what lets a step keep a text input and its
     * submit button both live.
     */
    function paintMasks(holes) {
        const vw = window.innerWidth;
        const vh = window.innerHeight;
        const boxes = [];
        for (const hole of holes) {
            const box = padded(hole, vw, vh);
            if (box) boxes.push(box);
        }

        const edges = [0, vh];
        for (const b of boxes) {
            edges.push(b.y, b.y + b.height);
        }
        const rows = Array.from(new Set(edges.filter((v) => v >= 0 && v <= vh)))
            .sort((a, b) => a - b);

        const rects = [];
        for (let i = 0; i < rows.length - 1; i++) {
            const top = rows[i];
            const bottom = rows[i + 1];
            if (bottom - top <= 0) continue;
            const mid = (top + bottom) / 2;
            // Spans of this band that a hole covers, merged left to right.
            const spans = boxes
                .filter((b) => b.y <= mid && b.y + b.height >= mid)
                .map((b) => [b.x, b.x + b.width])
                .sort((a, b) => a[0] - b[0]);
            let x = 0;
            for (const [from, to] of spans) {
                if (from > x) rects.push({ x: x, y: top, width: from - x, height: bottom - top });
                x = Math.max(x, to);
            }
            if (x < vw) rects.push({ x: x, y: top, width: vw - x, height: bottom - top });
        }

        const masks = pool(ui.masks, ui.maskLayer, 'tutorial-mask', rects.length);
        rects.forEach((rect, i) => place(masks[i], rect));

        const rings = pool(ui.rings, ui.ringLayer, 'tutorial-ring', boxes.length);
        boxes.forEach((box, i) => place(rings[i], box));
    }

    /** The union of the holes, which the card is positioned against. */
    function unionBox(holes) {
        if (!holes.length) return null;
        let x = Infinity, y = Infinity, right = -Infinity, bottom = -Infinity;
        for (const h of holes) {
            x = Math.min(x, h.x);
            y = Math.min(y, h.y);
            right = Math.max(right, h.x + h.width);
            bottom = Math.max(bottom, h.y + h.height);
        }
        return { x: x, y: y, width: right - x, height: bottom - y };
    }

    /**
     * Place the card so it never covers the spotlight: below, else above, else
     * beside it. The old last-resort branch dropped the card on top of the very
     * control the step was asking the user to click.
     */
    function paintCard(holes) {
        const card = ui.card;
        const vw = window.innerWidth;
        const vh = window.innerHeight;
        const box = card.getBoundingClientRect();
        const w = box.width || 320;
        const h = box.height || 160;
        const hole = unionBox(holes);
        if (!hole) {
            card.style.left = Math.round((vw - w) / 2) + 'px';
            card.style.top = Math.round((vh - h) / 2) + 'px';
            return;
        }
        const gap = 14;
        const clampX = (v) => Math.min(Math.max(8, v), Math.max(8, vw - w - 8));
        const clampY = (v) => Math.min(Math.max(8, v), Math.max(8, vh - h - 8));
        const centreX = clampX(hole.x + hole.width / 2 - w / 2);

        let left = centreX;
        let top;
        if (hole.y + hole.height + gap + h <= vh - 8) {
            top = hole.y + hole.height + gap;          // below
        } else if (hole.y - gap - h >= 8) {
            top = hole.y - gap - h;                    // above
        } else if (hole.x + hole.width + gap + w <= vw - 8) {
            left = hole.x + hole.width + gap;          // to the right
            top = clampY(hole.y);
        } else if (hole.x - gap - w >= 8) {
            left = hole.x - gap - w;                   // to the left
            top = clampY(hole.y);
        } else {
            // Nowhere clear: sit in whichever horizontal band is roomier.
            const above = hole.y;
            const below = vh - (hole.y + hole.height);
            top = above > below ? clampY(hole.y - gap - h) : clampY(hole.y + hole.height + gap);
        }
        card.style.left = Math.round(left) + 'px';
        card.style.top = Math.round(clampY(top)) + 'px';
    }

    function button(label, primary, onClick) {
        const node = document.createElement('button');
        node.type = 'button';
        node.className = primary ? 'tutorial-btn tutorial-btn-primary' : 'tutorial-btn';
        node.textContent = label;
        node.addEventListener('click', onClick);
        return node;
    }

    // ------------------------------------------------------------- interaction

    /** Nothing outside the spotlight or the card may take a key. */
    function onKeydown(event) {
        if (!current) return;
        if (event.key === 'Escape') {
            event.preventDefault();
            stop();
            return;
        }
        if (ui && ui.card.contains(event.target)) return;
        // Any host hole, not just the first: a step that spotlights a form must
        // let its text input be typed into, which is what made the Git Push
        // step impossible to complete.
        const holes = (current.holes || []);
        for (const hole of holes) {
            if (hole.node && hole.node.contains(event.target)) return;
        }
        event.preventDefault();
        event.stopPropagation();
    }

    // ------------------------------------------------------------------ engine

    function stepAt(index) {
        return current && current.steps[index] ? current.steps[index] : null;
    }

    function gotoStep(index) {
        current.index = index;
        current.enteredAt = now();
        // Recomputed on the next tick against the step now being entered.
        current.latched = null;
        scrolledFor = -1;
        save({ tour: current.tour, step: index, data: current.data, seen: current.seen });
    }

    /* The counter numbers the steps the user is actually shown, not positions in
     * the array. Steps whose `done` already holds — the panel is open, the
     * catalog is loaded — advance on the first tick without ever being
     * presented, which is what made the count read 1, 3, 5, 8, 9. `seen` maps a
     * step id to the number it was given, so Back and a resume on the next page
     * show the same number again rather than counting the step twice. */
    function stepNumber(step) {
        if (!step) return 0;
        if (current.seen[step.id] === undefined) {
            current.seen[step.id] = Object.keys(current.seen).length + 1;
            save({ tour: current.tour, step: current.index, data: current.data, seen: current.seen });
        }
        return current.seen[step.id];
    }

    /* Steps that cannot be skipped are the ones the user is guaranteed to see,
     * so the total is the count of those plus however many optional ones have
     * already been shown. It never shrinks under a number already displayed. */
    function stepTotal() {
        let total = 0;
        for (const step of current.steps) {
            // A `confirm` step waits for a press, so it is always shown and
            // always counts — like a step with no `done` at all.
            if (!step.done || step.confirm || current.seen[step.id] !== undefined) total++;
        }
        return Math.max(total, Object.keys(current.seen).length);
    }

    function gotoId(id) {
        const index = current.steps.findIndex((s) => s.id === id);
        // An unknown id used to end the tour silently, so a typo looked exactly
        // like "tutorial finished". Hold the step instead and say so.
        if (index < 0) {
            current.error = 'This tutorial cannot continue (unknown step "' + id + '").';
            return;
        }
        current.error = null;
        gotoStep(index);
    }

    function advance() {
        if (current.index + 1 >= current.steps.length) finish();
        else gotoStep(current.index + 1);
    }

    function finish() {
        stop();
    }

    function ctx() {
        return {
            frame: frameState(),
            data: current.data,
            page: page,
            waited: now() - (current.enteredAt || now())
        };
    }

    function tick() {
        if (!current) return;
        const step = stepAt(current.index);
        if (!step) {
            stop();
            return;
        }

        // The step belongs to the other page. Arriving on the page a later step
        // lives on *is* the completion of the navigation steps in between, so
        // jump ahead to the first step this page owns. With none, the user
        // navigated away from where the tutorial continues: keep the overlay up
        // and say where to return to.
        if (step.page && step.page !== page) {
            const ahead = current.steps.findIndex((s, i) => i > current.index && s.page === page);
            if (ahead >= 0) {
                gotoStep(ahead);
                return;
            }
            current.holes = [];
            render(step, [], {
                waiting: step.elsewhere || (step.page === 'app'
                    ? 'Open your application again to continue the tutorial.'
                    : 'Return to the application list to continue.')
            });
            return;
        }

        const c = ctx();

        // A precondition is a state the step needs before it can be attempted.
        // It renders its own spotlight and remedy text, and the step resumes on
        // its own once the state holds — this is what stops a collapsed
        // assistant sidebar from hanging the whole Notebook tutorial.
        if (step.precondition && !step.precondition.ok(c)) {
            /* A precondition the tutorial can satisfy itself does so instead of
             * waiting. The menu tours need this: the overlay swallows clicks,
             * so a dropdown the user is asked to look at can never be opened by
             * the user — the tutorial has to open it. `fix` runs once per entry
             * into the step and the tick re-evaluates immediately. */
            if (step.precondition.fix && current.fixed !== current.index) {
                current.fixed = current.index;
                step.precondition.fix(c);
                if (step.precondition.ok(ctx())) return tick();
            }
            const holes = resolveTargets(step.precondition, c);
            current.holes = holes;
            maybeScroll(step.precondition, holes, c);
            render(step.precondition, holes, {
                waiting: holes.length ? null : (step.precondition.missing || null)
            });
            return;
        }

        /* A step completes because the user did something, not because the
         * application happened to already be in that state. Starting the tour
         * with the panel left open from a previous run used to satisfy `open`
         * on its very first tick and skip straight past it, so the tutorial
         * appeared to begin at step 2 or 3 and its own instruction was never
         * read. A step that is already satisfied when entered is therefore
         * shown and latched, and advances on the next state change instead.
         * `settled` steps opt out: "Running" is a pure wait with no
         * instruction to read, so holding it would strand the user. */
        if (!current.error && step.done) {
            const satisfied = step.done(c);
            // Hold the decision until the state is knowable. A `frame:` step is
            // evaluated before the first bridge report, where done() is false
            // only because nothing has been heard yet; latching on that reads
            // as "not yet satisfied" and lets the very first report advance the
            // step — the skip this latch exists to prevent.
            if (current.latched === null && (!step.needsFrame || frameFresh())) {
                current.latched = satisfied && !step.settled;
            }
            /* A `confirm` step never advances on its own: its condition is
             * satisfied by something the application does rather than by the
             * user — the catalog finishing its own fetch — so auto-advancing
             * gave the user a single 150 ms tick to read the card before it
             * moved on. Once the condition holds it offers Next and waits for
             * the press. The latch above is a different case: it is about a
             * condition that is already true on arrival. */
            if (satisfied && current.latched === false && !step.confirm) {
                if (step.onDone) step.onDone(c);
                if (step.goto) gotoId(step.goto(c));
                else advance();
                return;
            }
            if (current.latched && !satisfied) current.latched = false;
        }
        if (!current.error && step.back && step.back(c)) {
            gotoStep(Math.max(0, current.index - 1));
            return;
        }

        let holes = resolveTargets(step, c);

        // The target vanished while its modal stayed open — a failed commit
        // hides the confirm buttons, a failed push hides the whole form. Fall
        // back to the modal so the error text and the buttons that recover from
        // it are readable and clickable instead of buried under the mask.
        if (!holes.length && step.fallback) {
            const fallback = hostRect(step.fallback);
            if (fallback) holes = [fallback];
        }

        current.holes = holes;
        maybeScroll(step, holes, c);

        let waiting = null;
        if (current.error) waiting = current.error;
        else if (step.wait) waiting = step.wait(c);
        else if (!holes.length && (step.target || step.targets)) waiting = step.missing || 'Waiting for the next control…';
        else if (holes.length && holes.every((h) => h.disabled)) waiting = step.blocked || 'Waiting until this becomes available…';

        render(step, holes, { waiting: waiting });
    }

    /**
     * Bring a target into view once per step. Host targets scroll directly;
     * frame targets are asked to scroll themselves, because wheel events over
     * the overlay scroll this document and never the iframe.
     */
    function maybeScroll(step, holes, c) {
        if (scrolledFor === current.index) return;
        const host = holes.find((h) => h.node);
        if (host) {
            scrolledFor = current.index;
            if (host.y < 0 || host.y + host.height > window.innerHeight) {
                host.node.scrollIntoView({ block: 'center', behavior: 'smooth' });
            }
            return;
        }
        const framed = holes.find((h) => h.frame && h.offscreen);
        if (framed) {
            scrolledFor = current.index;
            tellFrame({ type: 'scroll', target: framed.frameTarget, index: pickIndex(framed) });
            return;
        }
        // Nothing resolved yet: a frame target that is entirely off the fold is
        // not reported at all, so ask the frame to scroll it into view by name.
        if (!holes.length) {
            const spec = step.targets || (step.target ? [step.target] : []);
            for (let one of spec) {
                if (typeof one === 'function') one = one(c);
                if (typeof one === 'string' && one.indexOf('frame:') === 0) {
                    const rest = one.slice(6);
                    const at = rest.indexOf('@');
                    const name = at < 0 ? rest : rest.slice(0, at);
                    const pick = at < 0 ? 0 : Number(rest.slice(at + 1));
                    tellFrame({ type: 'scroll', target: name, index: Number.isInteger(pick) ? pick : 0 });
                    scrolledFor = current.index;
                    return;
                }
            }
        }
    }

    function pickIndex(hole) {
        return typeof hole.framePick === 'number' ? hole.framePick : 0;
    }

    /**
     * The waiting message a stalled step should show. A bridge that never
     * reports is a configuration problem — an old TruACP still running from a
     * launch that predates the tour bridge — and has to be named, because it is
     * indistinguishable from a step the user simply has not completed.
     */
    function diagnose(step, holes, waiting, c) {
        if (!step.needsFrame) return waiting;
        if (frameOutdated()) {
            return 'The assistant panel is running an older version that cannot ' +
                'drive this tutorial. Rebuild TruACP and relaunch the application.';
        }
        if (!frameFresh() && c.waited > FRAME_HELP_MS) {
            return 'The assistant panel is not responding. Reload the application ' +
                '(Utils ▸ Reload) and start the tutorial again.';
        }
        return waiting;
    }

    function render(step, holes, opts) {
        const c = ctx();
        paintMasks(holes);
        ui.step.textContent = 'Step ' + stepNumber(stepAt(current.index)) + ' of ' + stepTotal() +
            ' · ' + current.title;
        ui.title.textContent = typeof step.title === 'function' ? step.title(c) : step.title;
        const text = typeof step.text === 'function' ? step.text(c) : step.text;
        ui.text.textContent = text || '';
        ui.text.classList.toggle('hidden', !text);

        let waiting = diagnose(step, holes, opts.waiting, c);
        // With no hole and nothing to read, the overlay would be a blank white
        // wall. Always say what is being waited for.
        if (!waiting && !holes.length && !text) waiting = 'Waiting…';
        ui.wait.classList.toggle('hidden', !waiting);
        ui.waitText.textContent = waiting || '';

        // Actions are rebuilt each tick only when they changed, so a click is
        // never lost to a node replaced mid-press.
        //
        // These are deliberately NOT gated on `waiting`. Gating them meant an
        // explanation step whose button happened to be disabled — >| and >> are
        // disabled until a step is selected — lost its Next and left Exit as the
        // only way out.
        const wanted = [];
        if (step.next) wanted.push({ key: 'next', label: step.next, primary: true });
        // A `confirm` step waits for the press rather than advancing itself, so
        // its Next appears only once the condition actually holds — offering it
        // while the catalog is still loading would step over what it is for.
        else if (step.confirm && step.done && step.done(c)) {
            wanted.push({ key: 'next', label: step.confirm, primary: true });
        }
        if (step.choices) {
            step.choices.forEach((choice, i) => {
                wanted.push({ key: 'choice' + i, label: choice.label, primary: !!choice.primary, choice: choice });
            });
        }
        if (step.finish) wanted.push({ key: 'finish', label: step.finish, primary: true });

        const signature = wanted.map((w) => w.key + ':' + w.label).join('|');
        if (ui.actions.dataset.signature !== signature) {
            ui.actions.dataset.signature = signature;
            while (ui.actions.firstChild) ui.actions.removeChild(ui.actions.firstChild);
            wanted.forEach((w) => {
                ui.actions.appendChild(button(w.label, w.primary, () => {
                    if (w.key === 'finish') finish();
                    else if (w.choice) {
                        if (w.choice.goto) gotoId(w.choice.goto);
                        else advance();
                    } else advance();
                }));
            });
        }
        ui.actions.classList.toggle('hidden', wanted.length === 0);
        // Always available, so no step can strand the user — except on a final
        // step, where there is nothing after this one to skip to.
        ui.skip.classList.toggle('hidden', !!step.finish);

        paintCard(holes);
    }

    // ------------------------------------------------------------- public API

    let observers = [];

    // Repaints are coalesced onto an animation frame. Painting mutates the
    // overlay, which the MutationObserver below would otherwise see as another
    // reason to paint — a loop that never settles.
    let repaintQueued = false;

    function repaint() {
        if (repaintQueued || !current) return;
        repaintQueued = true;
        requestAnimationFrame(() => {
            repaintQueued = false;
            tick();
        });
    }

    function watch() {
        // The poll alone lets the hole lag anything that moves between ticks:
        // page scroll, the workbench divider drag, the app list re-rendering
        // its whole innerHTML. Repaint on the events that actually move things.
        window.addEventListener('resize', repaint);
        window.addEventListener('scroll', repaint, true);
        observers.push(() => {
            window.removeEventListener('resize', repaint);
            window.removeEventListener('scroll', repaint, true);
        });
        if (typeof ResizeObserver === 'function') {
            const ro = new ResizeObserver(repaint);
            [el('leftFrame'), el('appList')].forEach((node) => {
                if (node) ro.observe(node);
            });
            observers.push(() => ro.disconnect());
        }
        if (typeof MutationObserver === 'function') {
            const mo = new MutationObserver((records) => {
                // Ignore the overlay's own painting, or this observer feeds
                // itself for as long as the tutorial runs.
                for (const record of records) {
                    const node = record.target;
                    if (ui && ui.root.contains(node)) continue;
                    repaint();
                    return;
                }
            });
            mo.observe(document.body, {
                subtree: true,
                childList: true,
                attributes: true,
                attributeFilter: ['class', 'style', 'disabled']
            });
            observers.push(() => mo.disconnect());
        }
    }

    function unwatch() {
        observers.forEach((off) => off());
        observers = [];
    }

    function begin(tourId, state) {
        const tour = TOURS[tourId];
        if (!tour) return;
        if (current) stop();
        buildOverlay();
        current = {
            tour: tourId,
            title: tour.title,
            steps: tour.steps,
            index: (state && state.step) || 0,
            data: (state && state.data) || (tour.data ? tour.data() : {}),
            // Numbers already handed out, so a tutorial that crosses from
            // app.html to applist.html keeps counting where it left off.
            seen: (state && state.seen) || {},
            holes: [],
            enteredAt: now(),
            // null = not yet evaluated for this step; see the `done` check.
            latched: null,
            // Index of the step whose precondition `fix` has already run.
            fixed: -1,
            error: null
        };
        if (current.index >= current.steps.length) current.index = 0;
        scrolledFor = -1;
        save({ tour: tourId, step: current.index, data: current.data, seen: current.seen });
        ui.root.classList.add('active');
        document.addEventListener('keydown', onKeydown, true);
        tellFrame({ type: 'start' });
        tick();
        if (timer === null) timer = setInterval(() => {
            // The frame reloads with the app; re-asking every tick is cheap and
            // makes the bridge reconnect on its own.
            if (!frameFresh()) tellFrame({ type: 'start' });
            tick();
        }, TICK_MS);
        watch();
    }

    function start(tourId) {
        clear();
        begin(tourId, null);
    }

    function stop() {
        clear();
        // A tour that changed the page to describe it puts it back, however it
        // ends: the Menus tour opens dropdowns the user cannot close while the
        // overlay is up, so exiting mid-tour would strand one open.
        if (current && TOURS[current.tour] && TOURS[current.tour].cleanup) {
            TOURS[current.tour].cleanup();
        }
        current = null;
        if (timer !== null) {
            clearInterval(timer);
            timer = null;
        }
        document.removeEventListener('keydown', onKeydown, true);
        unwatch();
        tellFrame({ type: 'stop' });
        if (ui) ui.root.classList.remove('active');
    }

    /** Called on load by both pages: continues a tutorial across navigation. */
    function resume() {
        const state = load();
        if (!state || !TOURS[state.tour]) return;
        begin(state.tour, state);
    }

    // -------------------------------------------------------------- the tours

    /** The assistant iframe is hidden by the sidebar toggle, and the choice is
     *  remembered in localStorage — so a user who collapsed it once would find
     *  every Notebook step waiting on a frame that can never report. */
    const SIDEBAR_PRECONDITION = {
        ok: () => !!frameIframe(),
        target: '#sidebarToggleBtn',
        title: 'Show the assistant',
        text: 'The templates live in the assistant panel. Click here to show it.',
        missing: 'Waiting for the assistant panel…'
    };

    /* The Menus tour describes dropdowns, which the user cannot open while the
     * overlay is up: the masks swallow the click. So the tour opens the menu
     * itself through `fix` and closes the other one, keeping exactly the menu
     * being described on screen. `hidden` is the only state these menus have —
     * app.html toggles that class and nothing else — so this drives them the
     * same way the page does, without depending on its globals. */
    function menuOpen(id) {
        const node = el(id);
        return !!node && !node.classList.contains('hidden');
    }

    function setMenu(id, open) {
        const node = el(id);
        if (node) node.classList.toggle('hidden', !open);
    }

    function MENU_OPEN(menuId, buttonId) {
        const other = menuId === 'configMenu' ? 'utilsMenu' : 'configMenu';
        return {
            ok: () => menuOpen(menuId),
            fix: () => {
                setMenu(other, false);
                setMenu(menuId, true);
            },
            target: '#' + buttonId,
            title: 'Open the menu',
            text: 'Click here to open the menu.',
            missing: 'Waiting for the menu…'
        };
    }

    /** The last step leaves the bar as the tour found it. */
    const MENUS_CLOSED = {
        ok: () => !menuOpen('configMenu') && !menuOpen('utilsMenu'),
        fix: () => {
            setMenu('configMenu', false);
            setMenu('utilsMenu', false);
        },
        title: 'Closing the menus',
        missing: 'Closing the menus…'
    };

    const TOURS = {
        notebook: {
            title: 'Templates',
            steps: [
                {
                    id: 'open',
                    page: 'app',
                    precondition: SIDEBAR_PRECONDITION,
                    needsFrame: true,
                    target: 'frame:notebook-toggle',
                    title: 'Open Templates',
                    text: 'Click here to open your templates.',
                    missing: 'Waiting for the assistant panel to load…',
                    done: (c) => !!c.frame && c.frame.panelOpen === true
                },
                {
                    id: 'refresh',
                    page: 'app',
                    precondition: SIDEBAR_PRECONDITION,
                    needsFrame: true,
                    target: 'frame:notebook-refresh',
                    title: 'Load templates',
                    text: 'Refresh the list if your templates are not visible.',
                    blocked: 'Loading the template catalog…',
                    // The catalog loads on its own, so this step completes
                    // without the user doing anything: it waits for Next rather
                    // than advancing the moment the entries land.
                    confirm: 'Next',
                    done: (c) => !!c.frame && c.frame.entries > 0
                },
                {
                    id: 'configure',
                    page: 'app',
                    precondition: SIDEBAR_PRECONDITION,
                    needsFrame: true,
                    target: 'frame:notebook-source',
                    title: 'Configure templates',
                    text: 'From Configure you can create templates and connect your GitHub account. ' +
                        'Nothing to change here while your templates are already listed.',
                    next: 'Next'
                },
                {
                    id: 'select',
                    page: 'app',
                    precondition: SIDEBAR_PRECONDITION,
                    needsFrame: true,
                    // Prefer the named template the copy mentions, and fall back
                    // to the first entry when this catalog does not carry it.
                    targets: [(c) => {
                        const wanted = 'frame:notebook-entry@App Suite';
                        return resolveTarget(wanted, c) ? wanted : 'frame:notebook-entry';
                    }],
                    title: 'Select a template',
                    text: 'Select a template to start — for example App Suite.',
                    blocked: 'Loading…',
                    missing: 'Waiting for the template list…',
                    done: (c) => !!c.frame && c.frame.nodes > 0
                },
                {
                    id: 'close',
                    page: 'app',
                    precondition: SIDEBAR_PRECONDITION,
                    needsFrame: true,
                    target: 'frame:notebook-close',
                    title: 'Close the template list',
                    text: 'Close the panel to see the steps of the template.',
                    done: (c) => !!c.frame && c.frame.panelOpen === false
                },
                {
                    id: 'run',
                    page: 'app',
                    precondition: SIDEBAR_PRECONDITION,
                    needsFrame: true,
                    target: 'frame:notebook-node-run',
                    title: 'Run the first step',
                    text: 'Run the first step of the template.',
                    missing: 'Waiting for the template steps…',
                    done: (c) => !!c.frame &&
                        (c.frame.running === true || c.frame.firstNodeRunState === 'done')
                },
                {
                    id: 'wait',
                    page: 'app',
                    precondition: SIDEBAR_PRECONDITION,
                    needsFrame: true,
                    title: 'Running',
                    // A pure wait with nothing to read or do: when the run has
                    // already finished there is no reason to hold it.
                    settled: true,
                    wait: () => 'Waiting for the model to finish…',
                    done: (c) => !!c.frame &&
                        c.frame.running === false && c.frame.firstNodeRunState === 'done'
                },
                {
                    id: 'run-next',
                    page: 'app',
                    precondition: SIDEBAR_PRECONDITION,
                    needsFrame: true,
                    target: 'frame:run-next',
                    title: 'Run and move on',
                    text: 'Use this button to run the current step and move to the next one.',
                    next: 'Next'
                },
                {
                    id: 'run-all',
                    page: 'app',
                    precondition: SIDEBAR_PRECONDITION,
                    needsFrame: true,
                    target: 'frame:run-all',
                    title: 'Run the whole template',
                    text: 'Use this button to run the entire template.',
                    next: 'Next'
                },
                {
                    id: 'complete',
                    page: 'app',
                    title: 'Tutorial complete',
                    text: 'You now know how to open and run a template.',
                    finish: 'Close'
                }
            ]
        },

        menus: {
            title: 'Menus',
            cleanup: () => {
                setMenu('configMenu', false);
                setMenu('utilsMenu', false);
            },
            steps: [
                {
                    id: 'config',
                    page: 'app',
                    precondition: MENU_OPEN('configMenu', 'configBtn'),
                    targets: ['#configBtn', '#configMenu'],
                    title: 'The Config menu',
                    text: 'Config holds the settings of your application: its environment ' +
                        'variables, the skills the assistant can use, and its AGENTS.md.',
                    next: 'Next'
                },
                {
                    id: 'env',
                    page: 'app',
                    precondition: MENU_OPEN('configMenu', 'configBtn'),
                    target: '#configEnv',
                    title: 'Env',
                    text: 'Env edits the environment variables of your application, ' +
                        'separately for development and for production.',
                    next: 'Next'
                },
                {
                    id: 'skills',
                    page: 'app',
                    precondition: MENU_OPEN('configMenu', 'configBtn'),
                    target: '#configSkills',
                    title: 'Skills',
                    text: 'Skills adds ready-made abilities to the assistant working on ' +
                        'this application.',
                    next: 'Next'
                },
                {
                    id: 'agents',
                    page: 'app',
                    precondition: MENU_OPEN('configMenu', 'configBtn'),
                    target: '#configAgents',
                    title: 'AGENTS.md',
                    text: 'AGENTS.md is the instructions the assistant reads before working ' +
                        'on your application. Edit it to tell it how your project works.',
                    next: 'Next'
                },
                {
                    id: 'utils',
                    page: 'app',
                    precondition: MENU_OPEN('utilsMenu', 'utilsBtn'),
                    targets: ['#utilsBtn', '#utilsMenu'],
                    title: 'The Utils menu',
                    text: 'Utils holds the actions you run on the application itself, ' +
                        'from reloading it to inspecting its files.',
                    next: 'Next'
                },
                {
                    id: 'revert',
                    page: 'app',
                    precondition: MENU_OPEN('utilsMenu', 'utilsBtn'),
                    target: '#utilsRevert',
                    title: 'Revert',
                    text: 'Revert throws away uncommitted changes and returns the ' +
                        'application to its last commit. It stays disabled while there is ' +
                        'nothing to revert.',
                    next: 'Next'
                },
                {
                    id: 'reload-redeploy',
                    page: 'app',
                    precondition: MENU_OPEN('utilsMenu', 'utilsBtn'),
                    targets: ['#utilsReload', '#utilsRedeploy'],
                    title: 'Reload and Redeploy',
                    text: 'Reload refreshes the preview only. Redeploy rebuilds the ' +
                        'application on the server first, which is what you need after ' +
                        'changing dependencies or configuration.',
                    next: 'Next'
                },
                {
                    id: 'clean',
                    page: 'app',
                    precondition: MENU_OPEN('utilsMenu', 'utilsBtn'),
                    target: '#utilsClean',
                    title: 'Clean',
                    text: 'Clean removes the build artefacts of the application and ' +
                        'rebuilds it from scratch.',
                    next: 'Next'
                },
                {
                    id: 'debug',
                    page: 'app',
                    precondition: MENU_OPEN('utilsMenu', 'utilsBtn'),
                    target: '#utilsDebug',
                    title: 'Debug',
                    text: 'Debug opens the log of your running application in a separate ' +
                        'window, which is where build and runtime errors appear.',
                    next: 'Next'
                },
                {
                    id: 'files',
                    page: 'app',
                    precondition: MENU_OPEN('utilsMenu', 'utilsBtn'),
                    target: '#utilsFiles',
                    title: 'Files',
                    text: 'Files browses the source of your application read-only, so you ' +
                        'can look at what the assistant has written.',
                    next: 'Next'
                },
                {
                    id: 'upload',
                    page: 'app',
                    precondition: MENU_OPEN('utilsMenu', 'utilsBtn'),
                    target: '#utilsUpload',
                    title: 'Upload',
                    text: 'Upload adds a file to your application — an image or a document ' +
                        'you want the assistant to use.',
                    next: 'Next'
                },
                {
                    id: 'complete',
                    page: 'app',
                    // Leave the page as the tour found it.
                    precondition: MENUS_CLOSED,
                    title: 'Tutorial complete',
                    text: 'Config holds the settings of your application, Utils the actions ' +
                        'you run on it.',
                    finish: 'Close'
                }
            ]
        },

        toolbar: {
            title: 'Toolbar',
            steps: [
                {
                    id: 'pane',
                    page: 'app',
                    target: '#sidebarToggleBtn',
                    title: 'The assistant pane',
                    text: 'This button hides and shows the assistant pane on the left. ' +
                        'Hiding it gives the preview the full width; your session keeps running either way.',
                    next: 'Next'
                },
                {
                    id: 'terminal',
                    page: 'app',
                    target: '#terminalBtn',
                    title: 'The terminal',
                    text: 'Terminal opens a shell inside your application, below the preview. ' +
                        'Use it to run commands against the running app.',
                    next: 'Next'
                },
                {
                    id: 'commit',
                    page: 'app',
                    target: '#saveBtn',
                    title: 'Commit',
                    text: 'Commit saves the current state of your application to the workspace. ' +
                        'It stays disabled while there is nothing to save — the pill on its left ' +
                        'tells you whether anything has changed.',
                    next: 'Next'
                },
                {
                    id: 'device',
                    page: 'app',
                    // The three segments are one control, so the whole group is
                    // spotlighted rather than any single button.
                    target: '#deviceToggle',
                    title: 'Desktop, tablet and mobile',
                    text: 'These three buttons resize the preview to a desktop, a tablet (820x1180) ' +
                        'or a phone (390x844), so you can check how your application looks on each.',
                    next: 'Next'
                },
                {
                    id: 'route',
                    page: 'app',
                    target: '#routeBtn',
                    title: 'The route',
                    text: 'Route controls which page of your application the preview opens, ' +
                        'and lets you add query parameters. The current route is shown on the button.',
                    next: 'Next'
                },
                {
                    id: 'reload',
                    page: 'app',
                    target: '#reloadBtn',
                    title: 'Reload',
                    text: 'Reload refreshes the preview. Hold Shift while clicking to redeploy the ' +
                        'application instead, which rebuilds it before reloading.',
                    next: 'Next'
                },
                {
                    id: 'back',
                    page: 'app',
                    target: '#backBtn',
                    title: 'Back',
                    text: 'Back leaves the workbench and returns to the list of your applications.',
                    next: 'Next'
                },
                {
                    id: 'complete',
                    page: 'app',
                    title: 'Tutorial complete',
                    text: 'That is the toolbar: the assistant pane, the terminal, Commit, the ' +
                        'preview sizes, the route, Reload and Back.',
                    finish: 'Close'
                }
            ]
        }
    };

    window.Tutorial = {
        start: start,
        stop: stop,
        resume: resume,
        // Test hooks. The engine is polled on a timer and keeps its position in
        // a closure, so tests/tutorial-spotlight.test.mjs needs to step it by
        // hand and read where it got to.
        tickForTest: tick,
        stepIdForTest: () => {
            const step = current && stepAt(current.index);
            return step ? step.id : null;
        }
    };

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', resume);
    } else {
        resume();
    }
})();
