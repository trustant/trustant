/*
 * Interactive guided tutorials (spec/16-tutorial.md).
 *
 * A tutorial is a spotlight walkthrough: a semi-transparent white overlay
 * covers the whole application, a hole is cut around the one control the user
 * must act on, and a card next to it says what to do. The overlay itself is
 * what disables everything else — clicks land on the mask, never on the
 * application underneath — while the hole leaves the real control clickable,
 * including when it lives inside the TruACP iframe.
 *
 * Steps never advance on a click. They advance when the application state says
 * the action completed (the notebook panel really opened, the commit really
 * succeeded), which is polled on a timer so the tour follows the app rather
 * than replaying a fixed slideshow. Notebook state lives in the TruACP frame on
 * another origin and arrives through the tour bridge (trustable-acp
 * web/tour-bridge.ts).
 *
 * Loaded by app.html (which also owns the Tutorial menu) and by applist.html,
 * because the Commit and Push tutorial crosses from one page to the other and
 * resumes from sessionStorage.
 */
(function () {
    'use strict';

    const STORAGE_KEY = 'trustable.tutorial';
    const TICK_MS = 150;
    // Frame reports arrive every 200ms; anything older means the frame went
    // away (navigation, reload, hidden panel) and its targets are unusable.
    const FRAME_STALE_MS = 1500;
    const HOLE_PAD = 6;

    const page = window.location.pathname.endsWith('applist.html') ? 'applist'
        : window.location.pathname.endsWith('app.html') ? 'app'
        : 'other';

    // ---------------------------------------------------------------- helpers

    function el(id) {
        return document.getElementById(id);
    }

    // getClientRects rather than offsetParent: much of what the tutorials point
    // at lives inside `position: fixed` modals, where offsetParent lies.
    function visible(node) {
        return !!node && !node.classList.contains('hidden') && node.getClientRects().length > 0;
    }

    /** Modals are toggled by adding/removing `hidden`, not by unmounting. */
    function modalOpen(id) {
        const node = el(id);
        return !!node && !node.classList.contains('hidden');
    }

    function cookie(name) {
        const match = document.cookie.match(new RegExp('(^| )' + name + '=([^;]+)'));
        return match ? decodeURIComponent(match[2]) : '';
    }

    // ------------------------------------------------------------ frame bridge

    // Last report from the TruACP iframe: control rects in frame-viewport
    // coordinates plus the notebook state the steps wait on.
    const frame = { targets: {}, state: {}, at: 0 };

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

    function tellFrame(type) {
        const win = frameWindow();
        const origin = frameOrigin();
        if (!win || !origin) return;
        win.postMessage({ source: 'trustable-tour-host', type: type }, origin);
    }

    window.addEventListener('message', (event) => {
        const data = event.data;
        if (!data || data.source !== 'trustable-tour-frame' || data.type !== 'frame') return;
        if (frameOrigin() && event.origin !== frameOrigin()) return;
        frame.targets = data.targets || {};
        frame.state = data.state || {};
        frame.at = Date.now();
    });

    function frameFresh() {
        return Date.now() - frame.at < FRAME_STALE_MS;
    }

    function frameState() {
        return frameFresh() ? frame.state : {};
    }

    /** Frame rects are relative to the iframe viewport; shift into ours. */
    function frameRect(name) {
        if (!frameFresh()) return null;
        const rect = frame.targets[name];
        const iframe = el('leftFrame');
        if (!rect || !iframe || iframe.classList.contains('hidden')) return null;
        const box = iframe.getBoundingClientRect();
        return {
            x: box.left + rect.x,
            y: box.top + rect.y,
            width: rect.width,
            height: rect.height,
            disabled: !!rect.disabled,
            frame: true
        };
    }

    function hostRect(selector) {
        const node = document.querySelector(selector);
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

    /** A step target is either a host selector or `frame:<name>`. */
    function resolveTarget(target) {
        if (typeof target === 'function') target = target();
        if (!target) return null;
        if (target.indexOf('frame:') === 0) return frameRect(target.slice(6));
        return hostRect(target);
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
    let current = null;   // { tour, steps, index, state }
    let scrolledFor = -1; // step index a host target was last scrolled into view for

    function buildOverlay() {
        if (ui) return ui;
        const root = document.createElement('div');
        root.id = 'tutorialOverlay';
        root.innerHTML =
            '<div class="tutorial-mask" data-part="top"></div>' +
            '<div class="tutorial-mask" data-part="bottom"></div>' +
            '<div class="tutorial-mask" data-part="left"></div>' +
            '<div class="tutorial-mask" data-part="right"></div>' +
            '<div class="tutorial-ring"></div>' +
            '<div class="tutorial-card" role="dialog" aria-live="polite">' +
            '  <div class="tutorial-card-step"></div>' +
            '  <h4 class="tutorial-card-title"></h4>' +
            '  <p class="tutorial-card-text"></p>' +
            '  <div class="tutorial-card-wait"><span class="tutorial-spinner"></span><span class="tutorial-wait-text"></span></div>' +
            '  <div class="tutorial-card-actions"></div>' +
            '  <button type="button" class="tutorial-exit">Exit tutorial</button>' +
            '</div>';
        document.body.appendChild(root);
        ui = {
            root: root,
            masks: {
                top: root.querySelector('[data-part="top"]'),
                bottom: root.querySelector('[data-part="bottom"]'),
                left: root.querySelector('[data-part="left"]'),
                right: root.querySelector('[data-part="right"]')
            },
            ring: root.querySelector('.tutorial-ring'),
            card: root.querySelector('.tutorial-card'),
            step: root.querySelector('.tutorial-card-step'),
            title: root.querySelector('.tutorial-card-title'),
            text: root.querySelector('.tutorial-card-text'),
            wait: root.querySelector('.tutorial-card-wait'),
            waitText: root.querySelector('.tutorial-wait-text'),
            actions: root.querySelector('.tutorial-card-actions')
        };
        root.querySelector('.tutorial-exit').addEventListener('click', () => stop());
        return ui;
    }

    function place(node, box) {
        node.style.left = Math.max(0, box.left) + 'px';
        node.style.top = Math.max(0, box.top) + 'px';
        node.style.width = Math.max(0, box.width) + 'px';
        node.style.height = Math.max(0, box.height) + 'px';
    }

    /** Cut the hole by sizing the four masks around it; none means full cover. */
    function paintMasks(hole) {
        const vw = window.innerWidth;
        const vh = window.innerHeight;
        if (!hole) {
            place(ui.masks.top, { left: 0, top: 0, width: vw, height: vh });
            place(ui.masks.bottom, { left: 0, top: vh, width: vw, height: 0 });
            place(ui.masks.left, { left: 0, top: 0, width: 0, height: 0 });
            place(ui.masks.right, { left: vw, top: 0, width: 0, height: 0 });
            ui.ring.style.opacity = '0';
            return;
        }
        const x = Math.max(0, hole.x - HOLE_PAD);
        const y = Math.max(0, hole.y - HOLE_PAD);
        const w = Math.min(vw - x, hole.width + HOLE_PAD * 2);
        const h = Math.min(vh - y, hole.height + HOLE_PAD * 2);
        place(ui.masks.top, { left: 0, top: 0, width: vw, height: y });
        place(ui.masks.bottom, { left: 0, top: y + h, width: vw, height: vh - (y + h) });
        place(ui.masks.left, { left: 0, top: y, width: x, height: h });
        place(ui.masks.right, { left: x + w, top: y, width: vw - (x + w), height: h });
        place(ui.ring, { left: x, top: y, width: w, height: h });
        ui.ring.style.opacity = '1';
    }

    function paintCard(hole) {
        const card = ui.card;
        const vw = window.innerWidth;
        const vh = window.innerHeight;
        const box = card.getBoundingClientRect();
        const w = box.width || 320;
        const h = box.height || 160;
        if (!hole) {
            card.style.left = Math.round((vw - w) / 2) + 'px';
            card.style.top = Math.round((vh - h) / 2) + 'px';
            return;
        }
        const gap = 14;
        let top = hole.y + hole.height + gap;
        if (top + h > vh - 8) top = hole.y - h - gap;         // above instead
        if (top < 8) top = Math.min(vh - h - 8, hole.y + gap); // side by side
        let left = hole.x + hole.width / 2 - w / 2;
        left = Math.min(Math.max(8, left), vw - w - 8);
        card.style.left = Math.round(left) + 'px';
        card.style.top = Math.round(Math.max(8, top)) + 'px';
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
        const inCard = ui && ui.card.contains(event.target);
        const hole = current.hole;
        const inTarget = hole && hole.node && hole.node.contains(event.target);
        if (inCard || inTarget) return;
        event.preventDefault();
        event.stopPropagation();
    }

    // ------------------------------------------------------------------ engine

    function stepAt(index) {
        return current && current.steps[index] ? current.steps[index] : null;
    }

    function gotoStep(index) {
        current.index = index;
        scrolledFor = -1;
        save({ tour: current.tour, step: index, data: current.data });
    }

    function gotoId(id) {
        const index = current.steps.findIndex((s) => s.id === id);
        if (index >= 0) gotoStep(index);
        else finish();
    }

    function advance() {
        if (current.index + 1 >= current.steps.length) finish();
        else gotoStep(current.index + 1);
    }

    function finish() {
        stop();
    }

    function ctx() {
        return { frame: frameState(), data: current.data, page: page };
    }

    function tick() {
        if (!current) return;
        const step = stepAt(current.index);
        if (!step) {
            stop();
            return;
        }

        // The step belongs to the other page. Arriving on the page a later step
        // lives on *is* the completion of the navigation steps in between — the
        // Back step of the Commit and Push tutorial ends exactly this way — so
        // jump ahead to the first step this page owns. With none, the user
        // navigated away from where the tutorial continues: keep the overlay up
        // and say where to go back to.
        if (step.page && step.page !== page) {
            const ahead = current.steps.findIndex((s, i) => i > current.index && s.page === page);
            if (ahead >= 0) {
                gotoStep(ahead);
                return;
            }
            current.hole = null;
            render(step, null, {
                waiting: step.elsewhere || (step.page === 'app'
                    ? 'Open your application again to continue the tutorial.'
                    : 'Return to the application list to continue.')
            });
            return;
        }

        const c = ctx();
        if (step.done && step.done(c)) {
            if (step.onDone) step.onDone(c);
            if (step.goto) gotoId(step.goto(c));
            else advance();
            return;
        }
        if (step.back && step.back(c)) {
            gotoStep(Math.max(0, current.index - 1));
            return;
        }

        const hole = resolveTarget(step.target);
        current.hole = hole;

        // A host target below the fold is brought into view once per step, so
        // the spotlight is never off screen.
        if (hole && hole.node && scrolledFor !== current.index) {
            scrolledFor = current.index;
            if (hole.y < 0 || hole.y + hole.height > window.innerHeight) {
                hole.node.scrollIntoView({ block: 'center', behavior: 'smooth' });
            }
        }

        let waiting = null;
        if (step.wait) waiting = step.wait(c);
        else if (!hole && step.target) waiting = step.missing || 'Waiting for the next control…';
        else if (hole && hole.disabled) waiting = step.blocked || 'Waiting until this becomes available…';

        render(step, hole, { waiting: waiting });
    }

    function render(step, hole, opts) {
        const c = ctx();
        paintMasks(hole);
        ui.step.textContent = 'Step ' + (current.index + 1) + ' of ' + current.steps.length +
            ' · ' + current.title;
        ui.title.textContent = typeof step.title === 'function' ? step.title(c) : step.title;
        const text = typeof step.text === 'function' ? step.text(c) : step.text;
        ui.text.textContent = text || '';
        ui.text.classList.toggle('hidden', !text);

        const waiting = opts.waiting;
        ui.wait.classList.toggle('hidden', !waiting);
        ui.waitText.textContent = waiting || '';

        // Actions are rebuilt each tick only when they changed, so a click is
        // never lost to a node replaced mid-press.
        const wanted = [];
        if (!waiting && step.next) wanted.push({ key: 'next', label: step.next, primary: true });
        if (!waiting && step.choices) {
            step.choices.forEach((choice, i) => {
                wanted.push({ key: 'choice' + i, label: choice.label, primary: !!choice.primary, choice: choice });
            });
        }
        if (step.finish) wanted.push({ key: 'finish', label: step.finish, primary: true });
        const signature = wanted.map((w) => w.key + ':' + w.label).join('|');
        if (ui.actions.dataset.signature !== signature) {
            ui.actions.dataset.signature = signature;
            ui.actions.innerHTML = '';
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

        paintCard(hole);
    }

    // ------------------------------------------------------------- public API

    function begin(tourId, state) {
        const tour = TOURS[tourId];
        if (!tour) return;
        buildOverlay();
        current = {
            tour: tourId,
            title: tour.title,
            steps: tour.steps,
            index: (state && state.step) || 0,
            data: (state && state.data) || (tour.data ? tour.data() : {}),
            hole: null
        };
        if (current.index >= current.steps.length) current.index = 0;
        scrolledFor = -1;
        save({ tour: tourId, step: current.index, data: current.data });
        ui.root.classList.add('active');
        document.addEventListener('keydown', onKeydown, true);
        tellFrame('start');
        tick();
        if (timer === null) timer = setInterval(() => {
            // The frame reloads with the app; re-asking every tick is cheap and
            // makes the bridge reconnect on its own.
            if (!frameFresh()) tellFrame('start');
            tick();
        }, TICK_MS);
        window.addEventListener('resize', tick);
    }

    function start(tourId) {
        clear();
        begin(tourId, null);
    }

    function stop() {
        clear();
        current = null;
        if (timer !== null) {
            clearInterval(timer);
            timer = null;
        }
        document.removeEventListener('keydown', onKeydown, true);
        window.removeEventListener('resize', tick);
        tellFrame('stop');
        if (ui) ui.root.classList.remove('active');
    }

    /** Called on load by both pages: continues a tutorial across navigation. */
    function resume() {
        const state = load();
        if (!state || !TOURS[state.tour]) return;
        begin(state.tour, state);
    }

    // -------------------------------------------------------------- the tours

    const TOURS = {
        notebook: {
            title: 'Notebook',
            steps: [
                {
                    id: 'open',
                    page: 'app',
                    target: 'frame:notebook-toggle',
                    title: 'Open Notebook',
                    text: 'Click here to open your notebooks.',
                    missing: 'Waiting for the assistant panel to load…',
                    done: (c) => c.frame.panelOpen === true
                },
                {
                    id: 'refresh',
                    page: 'app',
                    target: 'frame:notebook-refresh',
                    title: 'Load notebooks',
                    text: 'Refresh the list if your notebooks are not visible.',
                    blocked: 'Loading the notebook catalog…',
                    // Already-loaded catalogs satisfy this step immediately.
                    done: (c) => c.frame.entries > 0
                },
                {
                    id: 'configure',
                    page: 'app',
                    target: 'frame:notebook-source',
                    title: 'Configure notebooks',
                    text: 'From Configure you can create notebooks and connect your GitHub account. ' +
                        'Nothing to change here while your notebooks are already listed.',
                    next: 'Next'
                },
                {
                    id: 'select',
                    page: 'app',
                    target: 'frame:notebook-entry',
                    title: 'Select a notebook',
                    text: 'Select a notebook to start — for example App Suite.',
                    blocked: 'Loading…',
                    done: (c) => c.frame.nodes > 0
                },
                {
                    id: 'close',
                    page: 'app',
                    target: 'frame:notebook-close',
                    title: 'Close the notebook list',
                    text: 'Close the panel to see the steps of the notebook.',
                    done: (c) => c.frame.panelOpen === false
                },
                {
                    id: 'run',
                    page: 'app',
                    target: 'frame:notebook-node-run',
                    title: 'Run the first step',
                    text: 'Run the first step of the notebook.',
                    missing: 'Waiting for the notebook steps…',
                    done: (c) => c.frame.running === true || c.frame.firstNodeRunState === 'done'
                },
                {
                    id: 'wait',
                    page: 'app',
                    title: 'Running',
                    wait: () => 'Waiting for the model to finish…',
                    done: (c) => c.frame.running === false && c.frame.firstNodeRunState === 'done'
                },
                {
                    id: 'run-next',
                    page: 'app',
                    target: 'frame:run-next',
                    title: 'Run and move on',
                    text: 'Use this button to run the current step and move to the next one.',
                    next: 'Next'
                },
                {
                    id: 'run-all',
                    page: 'app',
                    target: 'frame:run-all',
                    title: 'Run the whole notebook',
                    text: 'Use this button to run the entire notebook.',
                    next: 'Next'
                },
                {
                    id: 'complete',
                    page: 'app',
                    title: 'Tutorial complete',
                    text: 'You now know how to open and run a notebook.',
                    finish: 'Close'
                }
            ]
        },

        commitpush: {
            title: 'Commit and Push',
            data: () => ({ app: cookie('NAME') }),
            steps: [
                {
                    id: 'commit',
                    page: 'app',
                    target: '#saveBtn',
                    title: 'Commit',
                    text: 'Click Commit to save your application to the workspace.',
                    blocked: 'There is nothing to commit yet — change something in your application first.',
                    done: () => modalOpen('saveModal')
                },
                {
                    id: 'commit-confirm',
                    page: 'app',
                    target: '#saveConfirmBtn',
                    title: 'Confirm the commit',
                    text: 'Confirm to write the changes into the workspace repository.',
                    wait: () => visible(el('saveSpinner')) ? 'Committing…' : null,
                    // Only a green result counts: an error leaves the step in
                    // place so the user can read it and retry.
                    done: () => visible(el('saveResult')) &&
                        el('saveResultText').classList.contains('text-green-700'),
                    back: () => !modalOpen('saveModal')
                },
                {
                    id: 'commit-done',
                    page: 'app',
                    target: '#saveContinueBtn',
                    title: 'Committed',
                    text: 'Your application is saved to the workspace.',
                    done: () => !modalOpen('saveModal')
                },
                {
                    id: 'back',
                    page: 'app',
                    target: '#backBtn',
                    title: 'Back',
                    text: 'Go back to continue.',
                    elsewhere: 'Waiting for the application list…',
                    // Leaving the page is the completion: the tutorial picks up
                    // again from sessionStorage once applist.html loads.
                    done: () => false
                },
                {
                    id: 'choose',
                    page: 'applist',
                    target: (c) => '[data-tour-push="' + cssEscape(currentApp()) + '"]',
                    title: 'Choose what to do',
                    text: 'Push your committed application to a GitHub repository, or cancel and push it later.',
                    missing: 'Waiting for your application in the list…',
                    elsewhere: 'Return to the application list to continue.',
                    choices: [{ label: 'Cancel', goto: 'cancelled' }],
                    done: () => modalOpen('gitPushModal')
                },
                {
                    id: 'push',
                    page: 'applist',
                    // While the push runs there is nothing to click; when the
                    // repository is not configured yet the form appears and the
                    // spotlight moves onto its Push button.
                    target: () => visible(el('gitPushForm')) ? '#gitPushConfirmBtn' : null,
                    title: 'Push to GitHub',
                    text: () => visible(el('gitPushForm'))
                        ? 'Enter the GitHub repository and press Push.'
                        : '',
                    wait: () => {
                        if (visible(el('gitPushForm'))) return null;
                        if (visible(el('gitPushResult'))) return 'The push did not succeed — read the message and try again.';
                        return 'Pushing to GitHub…';
                    },
                    done: () => visible(el('gitPushResult')) &&
                        el('gitPushResultText').className.indexOf('nu-feedback-success') >= 0,
                    back: () => !modalOpen('gitPushModal')
                },
                {
                    id: 'pushed',
                    page: 'applist',
                    title: 'Tutorial complete',
                    text: 'Your application has been committed and pushed to GitHub.',
                    finish: 'Close'
                },
                {
                    id: 'cancelled',
                    page: 'applist',
                    title: 'Tutorial complete',
                    text: 'Your application has been committed to the workspace. You can push it to GitHub later.',
                    finish: 'Close'
                }
            ]
        }
    };

    function currentApp() {
        return (current && current.data && current.data.app) || '';
    }

    /** Attribute selectors are built from an app name, so quote it safely. */
    function cssEscape(value) {
        return String(value).replace(/["\\]/g, '\\$&');
    }

    window.Tutorial = { start: start, stop: stop, resume: resume };

    if (document.readyState === 'loading') {
        document.addEventListener('DOMContentLoaded', resume);
    } else {
        resume();
    }
})();
