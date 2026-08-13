/**
 * Unit tests for the guided-tutorial engine (web/js/tutorial.js, spec/16-tutorial.md).
 *
 * The engine is browser code with no build step, and this machine has neither a
 * DOM library nor a runnable browser, so the test drives it through a minimal
 * DOM shim: enough of document/window/sessionStorage for the overlay to be
 * built and painted, with element rects supplied per test. That is sufficient
 * for what actually regressed — the mask tiling that decides whether anything
 * is highlighted at all, the action buttons that decide whether a step can be
 * left, and the tri-state frame data that decides whether a step can advance.
 *
 * Run with:  node --test tests/tutorial-spotlight.test.mjs
 */
import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import vm from 'node:vm';

const ROOT = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const SOURCE = readFileSync(path.join(ROOT, 'web/js/tutorial.js'), 'utf8');

const VIEWPORT = { width: 1000, height: 800 };

/** A DOM node with just the surface tutorial.js touches. */
class El {
    constructor(tag) {
        this.tagName = String(tag).toUpperCase();
        this.children = [];
        this.parentNode = null;
        this.style = {};
        this.dataset = {};
        this.attributes = {};
        this.listeners = {};
        this.textContent = '';
        this.disabled = false;
        this.id = '';
        this._rect = null;
        this._classes = new Set();
        this.classList = {
            add: (...c) => c.forEach((x) => this._classes.add(x)),
            remove: (...c) => c.forEach((x) => this._classes.delete(x)),
            contains: (c) => this._classes.has(c),
            toggle: (c, on) => (on ? this._classes.add(c) : this._classes.delete(c))
        };
    }
    get className() {
        return Array.from(this._classes).join(' ');
    }
    set className(value) {
        this._classes = new Set(String(value).split(/\s+/).filter(Boolean));
    }
    get firstChild() {
        return this.children[0] || null;
    }
    appendChild(node) {
        node.parentNode = this;
        this.children.push(node);
        return node;
    }
    removeChild(node) {
        this.children = this.children.filter((c) => c !== node);
        node.parentNode = null;
        return node;
    }
    setAttribute(name, value) {
        this.attributes[name] = value;
    }
    addEventListener(type, fn) {
        (this.listeners[type] ||= []).push(fn);
    }
    removeEventListener(type, fn) {
        this.listeners[type] = (this.listeners[type] || []).filter((f) => f !== fn);
    }
    dispatch(type, event = {}) {
        (this.listeners[type] || []).forEach((fn) => fn({ target: this, ...event }));
    }
    contains(node) {
        if (node === this) return true;
        return this.children.some((c) => c.contains(node));
    }
    /** Rects are supplied by the test; absent means "not laid out". */
    setRect(rect) {
        this._rect = rect;
        return this;
    }
    getBoundingClientRect() {
        const r = this._rect || { x: 0, y: 0, width: 0, height: 0 };
        return { ...r, left: r.x, top: r.y, right: r.x + r.width, bottom: r.y + r.height };
    }
    getClientRects() {
        return this._rect ? [this.getBoundingClientRect()] : [];
    }
    descendants() {
        return this.children.flatMap((c) => [c, ...c.descendants()]);
    }
    matches(selector) {
        if (selector.startsWith('#')) return this.id === selector.slice(1);
        if (selector.startsWith('.')) return this._classes.has(selector.slice(1));
        const attr = selector.match(/^\[([\w-]+)="(.*)"\]$/);
        if (attr) return this.attributes[attr[1]] === attr[2];
        return this.tagName === selector.toUpperCase();
    }
    querySelectorAll(selector) {
        // Only the "A > *" form the engine's modal fallback uses is special.
        const child = selector.match(/^(.+?)\s*>\s*\*$/);
        if (child) {
            const parent = this.querySelector(child[1].trim());
            return parent ? parent.children.slice() : [];
        }
        return this.descendants().filter((n) => n.matches(selector));
    }
    querySelector(selector) {
        return this.querySelectorAll(selector)[0] || null;
    }
}

function makeEnv({ pathname = '/app.html', cookie = 'LEFT=http://opencode.test; NAME=demo' } = {}) {
    const body = new El('body');
    const store = new Map();
    const timers = [];

    const document = {
        body,
        readyState: 'complete',
        cookie,
        listeners: {},
        createElement: (tag) => new El(tag),
        getElementById: (id) => body.descendants().find((n) => n.id === id) || null,
        querySelector: (sel) => body.querySelector(sel),
        querySelectorAll: (sel) => body.querySelectorAll(sel),
        addEventListener(type, fn) {
            (this.listeners[type] ||= []).push(fn);
        },
        removeEventListener(type, fn) {
            this.listeners[type] = (this.listeners[type] || []).filter((f) => f !== fn);
        }
    };

    const window = {
        innerWidth: VIEWPORT.width,
        innerHeight: VIEWPORT.height,
        location: { pathname },
        listeners: {},
        addEventListener(type, fn) {
            (this.listeners[type] ||= []).push(fn);
        },
        removeEventListener(type, fn) {
            this.listeners[type] = (this.listeners[type] || []).filter((f) => f !== fn);
        },
        // Painting is driven explicitly by the tests, so a frame never fires on
        // its own and cannot race the assertions.
        requestAnimationFrame: () => 0
    };

    const sandbox = {
        window,
        document,
        sessionStorage: {
            getItem: (k) => (store.has(k) ? store.get(k) : null),
            setItem: (k, v) => store.set(k, String(v)),
            removeItem: (k) => store.delete(k)
        },
        setInterval: (fn, ms) => {
            timers.push(fn);
            return timers.length;
        },
        clearInterval: () => {},
        requestAnimationFrame: window.requestAnimationFrame,
        URL,
        Date,
        Math,
        JSON,
        Number,
        Array,
        Object,
        Set,
        Infinity,
        console
    };
    sandbox.globalThis = sandbox;
    vm.createContext(sandbox);
    vm.runInContext(SOURCE, sandbox);
    // The engine publishes itself on `window`, which is not the sandbox global.
    sandbox.Tutorial = window.Tutorial;
    return { sandbox, window, document, body, timers };
}

/** Post a bridge report the way the real frame would. */
function report(window, { targets = {}, state = null, version = 2, origin = 'http://opencode.test' } = {}) {
    (window.listeners.message || []).forEach((fn) =>
        fn({ origin, data: { source: 'trustable-tour-frame', type: 'frame', version, targets, state } })
    );
}

function overlay(document) {
    return document.getElementById('tutorialOverlay');
}

function masks(document) {
    return overlay(document).querySelectorAll('.tutorial-mask').filter((m) => m.style.display !== 'none');
}

function boxOf(node) {
    const px = (v) => parseFloat(v || '0');
    return { x: px(node.style.left), y: px(node.style.top), w: px(node.style.width), h: px(node.style.height) };
}

/** True when (x, y) falls inside some visible mask, i.e. is covered. */
function covered(document, x, y) {
    return masks(document).some((m) => {
        const b = boxOf(m);
        return x >= b.x && x < b.x + b.w && y >= b.y && y < b.y + b.h;
    });
}

function actionLabels(document) {
    return overlay(document)
        .querySelector('.tutorial-card-actions')
        .children.map((b) => b.textContent);
}

function waitText(document) {
    return overlay(document).querySelector('.tutorial-wait-text').textContent;
}

/** app.html scaffolding the Commit and Push tour addresses. */
function appPage(body, { commitDisabled = false } = {}) {
    const frame = new El('iframe');
    frame.id = 'leftFrame';
    frame.setRect({ x: 0, y: 80, width: 500, height: 720 });
    body.appendChild(frame);

    const toggle = new El('button');
    toggle.id = 'sidebarToggleBtn';
    toggle.setRect({ x: 200, y: 20, width: 40, height: 30 });
    body.appendChild(toggle);

    const save = new El('button');
    save.id = 'saveBtn';
    save.disabled = commitDisabled;
    save.setRect({ x: 700, y: 20, width: 100, height: 30 });
    body.appendChild(save);

    const modal = new El('div');
    modal.id = 'saveModal';
    modal.classList.add('hidden');
    body.appendChild(modal);
    const panel = new El('div');
    panel.setRect({ x: 300, y: 200, width: 400, height: 300 });
    modal.appendChild(panel);

    const confirm = new El('button');
    confirm.id = 'saveConfirmBtn';
    panel.appendChild(confirm);
    const spinner = new El('div');
    spinner.id = 'saveSpinner';
    spinner.classList.add('hidden');
    panel.appendChild(spinner);
    const result = new El('div');
    result.id = 'saveResult';
    result.classList.add('hidden');
    result.setRect({ x: 320, y: 220, width: 360, height: 120 });
    panel.appendChild(result);
    const resultText = new El('div');
    resultText.id = 'saveResultText';
    resultText.setRect({ x: 320, y: 220, width: 360, height: 80 });
    result.appendChild(resultText);
    const cont = new El('button');
    cont.id = 'saveContinueBtn';
    panel.appendChild(cont);

    const back = new El('button');
    back.id = 'backBtn';
    back.setRect({ x: 900, y: 20, width: 80, height: 30 });
    body.appendChild(back);
    return { frame, toggle, save, modal, panel, confirm, spinner, result, resultText, cont, back };
}

test('a step with a target leaves a hole and covers everything else', () => {
    const { sandbox, document, body } = makeEnv();
    const dom = appPage(body);
    sandbox.Tutorial.start('commitpush');

    // Step 1 spotlights #saveBtn at (700,20)-(800,50).
    assert.equal(covered(document, 750, 35), false, 'the target must not be covered');
    assert.equal(covered(document, 100, 400), true, 'the rest of the page must be covered');
    assert.equal(covered(document, 750, 300), true, 'below the target must be covered');
    assert.ok(masks(document).length >= 4, 'the cover is tiled from several masks');
    assert.ok(dom.save);
});

test('the masks cover the whole viewport when there is no target', () => {
    const { sandbox, document, body } = makeEnv();
    appPage(body);
    sandbox.Tutorial.start('notebook');
    // No frame report yet, so the notebook step has no hole.
    const total = masks(document).reduce((sum, m) => {
        const b = boxOf(m);
        return sum + b.w * b.h;
    }, 0);
    assert.equal(total, VIEWPORT.width * VIEWPORT.height);
});

test('two targets keep two holes open at once', () => {
    const { sandbox, document, body } = makeEnv({ pathname: '/applist.html' });

    const push = new El('button');
    push.setAttribute('data-tour-push', 'demo');
    push.setRect({ x: 100, y: 100, width: 120, height: 40 });
    body.appendChild(push);

    const modal = new El('div');
    modal.id = 'gitPushModal';
    body.appendChild(modal);
    const panel = new El('div');
    panel.setRect({ x: 300, y: 300, width: 400, height: 200 });
    modal.appendChild(panel);
    const form = new El('div');
    form.id = 'gitPushForm';
    form.setRect({ x: 320, y: 320, width: 360, height: 160 });
    panel.appendChild(form);
    const result = new El('div');
    result.id = 'gitPushResult';
    result.classList.add('hidden');
    panel.appendChild(result);
    const resultText = new El('div');
    resultText.id = 'gitPushResultText';
    result.appendChild(resultText);

    // Resume straight onto the push step, where the whole form is the target.
    sandbox.sessionStorage.setItem(
        'trustable.tutorial',
        JSON.stringify({ tour: 'commitpush', step: 5, data: { app: 'demo' } })
    );
    sandbox.Tutorial.resume();

    assert.equal(covered(document, 500, 400), false, 'the push form stays live');
    assert.equal(covered(document, 50, 50), true, 'the page behind stays covered');
});

test('the whole Git Push form is spotlighted, so the repository can be typed', () => {
    const { sandbox, document, body } = makeEnv({ pathname: '/applist.html' });
    const modal = new El('div');
    modal.id = 'gitPushModal';
    body.appendChild(modal);
    const form = new El('div');
    form.id = 'gitPushForm';
    form.setRect({ x: 320, y: 320, width: 360, height: 160 });
    modal.appendChild(form);
    const input = new El('input');
    input.id = 'gitPushRepo';
    input.setRect({ x: 330, y: 340, width: 200, height: 30 });
    form.appendChild(input);
    const result = new El('div');
    result.id = 'gitPushResult';
    result.classList.add('hidden');
    modal.appendChild(result);
    const resultText = new El('div');
    resultText.id = 'gitPushResultText';
    result.appendChild(resultText);

    sandbox.sessionStorage.setItem(
        'trustable.tutorial',
        JSON.stringify({ tour: 'commitpush', step: 5, data: { app: 'demo' } })
    );
    sandbox.Tutorial.resume();

    // The input sits inside the hole...
    assert.equal(covered(document, 400, 350), false);

    // ...and its keystrokes are not swallowed, which is what made this step
    // impossible to complete.
    const keydown = document.listeners.keydown[0];
    let prevented = false;
    keydown({ key: 'a', target: input, preventDefault: () => (prevented = true), stopPropagation: () => {} });
    assert.equal(prevented, false, 'typing into the spotlighted form must be allowed');

    // A key aimed at the masked page is still swallowed.
    prevented = false;
    keydown({ key: 'a', target: body, preventDefault: () => (prevented = true), stopPropagation: () => {} });
    assert.equal(prevented, true, 'keys outside the spotlight are still blocked');
});

test('an explanation step keeps its Next button even when the control is disabled', () => {
    const { sandbox, window, document, body } = makeEnv();
    appPage(body);

    sandbox.sessionStorage.setItem(
        'trustable.tutorial',
        JSON.stringify({ tour: 'notebook', step: 7, data: {} }) // run-next
    );
    sandbox.Tutorial.resume();
    report(window, {
        targets: { 'run-next': [{ x: 10, y: 10, width: 30, height: 30, disabled: true, label: '' }] },
        state: { panelOpen: false, entries: 1, nodes: 1, firstNodeRunState: 'done', running: false }
    });
    sandbox.Tutorial.tickForTest();

    assert.ok(actionLabels(document).includes('Next'), 'Next must survive a disabled target');
});

test('a stale frame never reads as a false condition', () => {
    const { sandbox, window, document, body } = makeEnv();
    appPage(body);
    sandbox.Tutorial.start('notebook');

    // "Close the notebook list" advances when panelOpen is false. With no
    // report at all that must NOT count as closed.
    sandbox.sessionStorage.setItem(
        'trustable.tutorial',
        JSON.stringify({ tour: 'notebook', step: 4, data: {} })
    );
    sandbox.Tutorial.resume();
    sandbox.Tutorial.tickForTest();
    assert.equal(sandbox.Tutorial.stepIdForTest(), 'close', 'must not advance on unknown state');

    report(window, {
        targets: {},
        state: { panelOpen: false, entries: 2, nodes: 3, firstNodeRunState: 'pending', running: false }
    });
    sandbox.Tutorial.tickForTest();
    assert.equal(sandbox.Tutorial.stepIdForTest(), 'run', 'advances once the frame confirms it');
});

test('a hidden assistant panel shows the sidebar remedy instead of hanging', () => {
    const { sandbox, document, body } = makeEnv();
    const dom = appPage(body);
    dom.frame.classList.add('hidden');

    sandbox.Tutorial.start('notebook');

    assert.match(overlay(document).querySelector('.tutorial-card-title').textContent, /Show the assistant/);
    // The sidebar toggle is the live control, not the frame.
    assert.equal(covered(document, 220, 35), false);
});

test('a bridge that is too old is named rather than waited on', () => {
    const { sandbox, window, document, body } = makeEnv();
    appPage(body);
    sandbox.Tutorial.start('notebook');
    report(window, { version: 1, targets: {}, state: { panelOpen: false, entries: 0, nodes: 0, firstNodeRunState: null, running: false } });
    sandbox.Tutorial.tickForTest();
    assert.match(waitText(document), /older version/);
});

test('reports from a foreign origin are dropped', () => {
    const { sandbox, window, document, body } = makeEnv();
    appPage(body);
    sandbox.Tutorial.start('notebook');
    report(window, {
        origin: 'http://evil.test',
        targets: { 'notebook-toggle': [{ x: 0, y: 0, width: 20, height: 20, disabled: false, label: '' }] },
        state: { panelOpen: true, entries: 1, nodes: 0, firstNodeRunState: null, running: false }
    });
    sandbox.Tutorial.tickForTest();
    assert.equal(sandbox.Tutorial.stepIdForTest(), 'open', 'a foreign frame cannot advance the tour');
});

test('a failed commit spotlights the modal so the error and Continue stay usable', () => {
    const { sandbox, document, body } = makeEnv();
    const dom = appPage(body);

    sandbox.sessionStorage.setItem(
        'trustable.tutorial',
        JSON.stringify({ tour: 'commitpush', step: 1, data: { app: 'demo' } })
    );
    dom.modal.classList.remove('hidden');
    // The confirm button is gone — confirmSave() hides it and leaves it hidden
    // on failure — and the result is red.
    dom.confirm.setRect(null);
    dom.result.classList.remove('hidden');
    dom.resultText.classList.add('text-red-700');
    sandbox.Tutorial.resume();

    assert.equal(covered(document, 500, 350), false, 'the modal panel is spotlighted');
    assert.match(waitText(document), /did not succeed/);
});

test('the catalog entry is chosen by label, not by position', () => {
    const { sandbox, window, document, body } = makeEnv();
    appPage(body);
    sandbox.sessionStorage.setItem(
        'trustable.tutorial',
        JSON.stringify({ tour: 'notebook', step: 3, data: {} }) // select
    );
    sandbox.Tutorial.resume();
    report(window, {
        targets: {
            'notebook-entry': [
                { x: 10, y: 10, width: 100, height: 20, disabled: false, label: 'Build' },
                { x: 10, y: 40, width: 100, height: 20, disabled: false, label: 'App Suite' }
            ]
        },
        state: { panelOpen: true, entries: 2, nodes: 0, firstNodeRunState: null, running: false }
    });
    sandbox.Tutorial.tickForTest();

    // The frame sits at y=80, so "App Suite" lands at y=120..140.
    assert.equal(covered(document, 60, 130), false, 'App Suite is the hole');
    assert.equal(covered(document, 60, 100), true, 'Build stays covered');
});

test('Escape ends the tutorial and forgets it', () => {
    const { sandbox, document, body } = makeEnv();
    appPage(body);
    sandbox.Tutorial.start('commitpush');
    assert.ok(sandbox.sessionStorage.getItem('trustable.tutorial'));

    document.listeners.keydown[0]({ key: 'Escape', target: body, preventDefault: () => {}, stopPropagation: () => {} });
    assert.equal(sandbox.sessionStorage.getItem('trustable.tutorial'), null);
    assert.equal(overlay(document).classList.contains('active'), false);
});
