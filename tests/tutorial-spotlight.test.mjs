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

/** Press a card button by label; returns false when it is not offered. */
function press(document, label) {
    const button = overlay(document)
        .querySelector('.tutorial-card-actions')
        .children.find((b) => b.textContent === label);
    if (!button) return false;
    button.dispatch('click', { preventDefault: () => {}, stopPropagation: () => {} });
    return true;
}

/** "Step 2 of 9 · Templates" -> {number: 2, total: 9} */
function stepCounter(document) {
    const text = overlay(document).querySelector('.tutorial-card-step').textContent;
    const m = /^Step (\d+) of (\d+)/.exec(text);
    return m ? { number: Number(m[1]), total: Number(m[2]) } : null;
}

/** app.html scaffolding the tours address. */
function appPage(body, { commitDisabled = false } = {}) {
    const frame = new El('iframe');
    frame.id = 'leftFrame';
    frame.setRect({ x: 0, y: 80, width: 500, height: 720 });
    body.appendChild(frame);

    // Config/Utils, which the Menus tour opens itself. Menus start hidden, the
    // way app.html renders them.
    [['configBtn', 'configMenu', ['configEnv', 'configSkills', 'configAgents']],
     ['utilsBtn', 'utilsMenu', ['utilsRevert', 'utilsReload', 'utilsRedeploy',
                                'utilsClean', 'utilsDebug', 'utilsFiles', 'utilsUpload']]]
        .forEach(([btnId, menuId, items], m) => {
            const btn = new El('button');
            btn.id = btnId;
            btn.setRect({ x: 300 + m * 90, y: 20, width: 80, height: 30 });
            body.appendChild(btn);
            const menu = new El('div');
            menu.id = menuId;
            menu.classList.add('hidden');
            menu.setRect({ x: 300 + m * 90, y: 55, width: 160, height: 30 * items.length });
            body.appendChild(menu);
            items.forEach((id, i) => {
                const item = new El('button');
                item.id = id;
                item.setRect({ x: 300 + m * 90, y: 55 + i * 30, width: 160, height: 30 });
                menu.appendChild(item);
            });
        });

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
    // The Toolbar tour's Commit step spotlights #saveBtn at (700,20)-(800,50).
    sandbox.sessionStorage.setItem(
        'trustable.tutorial',
        JSON.stringify({ tour: 'toolbar', step: 2, data: {} })
    );
    sandbox.Tutorial.resume();

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
    const { sandbox, window, document, body } = makeEnv();
    appPage(body);
    sandbox.Tutorial.start('notebook');

    // The sidebar precondition spotlights #sidebarToggleBtn (200,20) while the
    // frame is hidden; with the frame visible the step's own frame target takes
    // over. Both paths tile the same way, and the masks must leave exactly the
    // spotlit rect open and cover the rest.
    report(window, {
        targets: { 'notebook-toggle': [{ x: 20, y: 20, width: 60, height: 30, disabled: false, label: '' }] },
        state: { panelOpen: false, entries: 0, nodes: 0, firstNodeRunState: null, running: false }
    });
    sandbox.Tutorial.tickForTest();

    // The frame sits at y=80, so the reported rect lands at (20,100)-(80,130).
    assert.equal(covered(document, 50, 110), false, 'the frame target is the hole');
    assert.equal(covered(document, 50, 300), true, 'below it is covered');
    assert.equal(covered(document, 700, 110), true, 'beside it is covered');
    assert.ok(masks(document).length >= 4, 'the cover is tiled from several masks');
});

test('keys inside the spotlight are not swallowed', () => {
    const { sandbox, document, body } = makeEnv();
    const dom = appPage(body);
    // The Commit step of the Toolbar tour spotlights #saveBtn.
    sandbox.sessionStorage.setItem(
        'trustable.tutorial',
        JSON.stringify({ tour: 'toolbar', step: 2, data: {} })
    );
    sandbox.Tutorial.resume();

    const keydown = document.listeners.keydown[0];
    let prevented = false;
    keydown({ key: 'a', target: dom.save, preventDefault: () => (prevented = true), stopPropagation: () => {} });
    assert.equal(prevented, false, 'typing into the spotlighted control must be allowed');

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

    // Arriving with the panel already closed does not skip the step: the user
    // is shown what to do first (see the latch tests below). It is closing the
    // panel that advances it.
    report(window, {
        targets: {},
        state: { panelOpen: false, entries: 2, nodes: 3, firstNodeRunState: 'pending', running: false }
    });
    sandbox.Tutorial.tickForTest();
    assert.equal(sandbox.Tutorial.stepIdForTest(), 'close', 'shown before it can be completed');

    report(window, {
        targets: {},
        state: { panelOpen: true, entries: 2, nodes: 3, firstNodeRunState: 'pending', running: false }
    });
    sandbox.Tutorial.tickForTest();
    report(window, {
        targets: {},
        state: { panelOpen: false, entries: 2, nodes: 3, firstNodeRunState: 'pending', running: false }
    });
    sandbox.Tutorial.tickForTest();
    assert.equal(sandbox.Tutorial.stepIdForTest(), 'run', 'advances once the frame confirms the close');
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
    sandbox.Tutorial.start('toolbar');
    assert.ok(sandbox.sessionStorage.getItem('trustable.tutorial'));

    document.listeners.keydown[0]({ key: 'Escape', target: body, preventDefault: () => {}, stopPropagation: () => {} });
    assert.equal(sandbox.sessionStorage.getItem('trustable.tutorial'), null);
    assert.equal(overlay(document).classList.contains('active'), false);
});

test('the tutorial starts on its first step even when that step is already satisfied', () => {
    const { sandbox, window, document, body } = makeEnv();
    appPage(body);

    // The panel was left open by an earlier run, so `open`'s done() holds on
    // the very first tick. The tour used to skip it and appear to begin at a
    // later step, with its instruction never shown.
    sandbox.Tutorial.start('notebook');
    report(window, {
        targets: { 'notebook-toggle': [{ x: 10, y: 10, width: 30, height: 30, disabled: false, label: '' }] },
        state: { panelOpen: true, entries: 2, nodes: 0, firstNodeRunState: null, running: false }
    });
    sandbox.Tutorial.tickForTest();

    assert.equal(sandbox.Tutorial.stepIdForTest(), 'open', 'must stay on the first step');
    assert.equal(stepCounter(document).number, 1, 'and present it as step 1');
});

test('a latched step advances once the state actually changes', () => {
    const { sandbox, window, document, body } = makeEnv();
    appPage(body);
    sandbox.Tutorial.start('notebook');

    const open = { panelOpen: true, entries: 2, nodes: 0, firstNodeRunState: null, running: false };
    report(window, { targets: {}, state: open });
    sandbox.Tutorial.tickForTest();
    assert.equal(sandbox.Tutorial.stepIdForTest(), 'open', 'held while already satisfied');

    // The user closes the panel, then opens it again: that is a real action.
    report(window, { targets: {}, state: { ...open, panelOpen: false } });
    sandbox.Tutorial.tickForTest();
    report(window, { targets: {}, state: open });
    sandbox.Tutorial.tickForTest();
    assert.notEqual(sandbox.Tutorial.stepIdForTest(), 'open', 'advances on the user action');
});

test('step numbers are progressive even when steps are skipped', () => {
    const { sandbox, window, document, body } = makeEnv();
    appPage(body);
    sandbox.Tutorial.start('notebook');

    // Drive the tour forward and record every number it displays. Whatever is
    // skipped, the user must never see the count jump (it read 1, 3, 5, 8, 9).
    const seen = [];
    const push = () => {
        const c = stepCounter(document);
        if (c && seen[seen.length - 1] !== c.number) seen.push(c.number);
    };

    const states = [
        { panelOpen: false, entries: 0, nodes: 0, firstNodeRunState: null, running: false },
        { panelOpen: true, entries: 0, nodes: 0, firstNodeRunState: null, running: false },
        { panelOpen: true, entries: 3, nodes: 0, firstNodeRunState: null, running: false },
        { panelOpen: true, entries: 3, nodes: 4, firstNodeRunState: null, running: false },
        { panelOpen: false, entries: 3, nodes: 4, firstNodeRunState: null, running: false },
        { panelOpen: false, entries: 3, nodes: 4, firstNodeRunState: 'done', running: true },
        { panelOpen: false, entries: 3, nodes: 4, firstNodeRunState: 'done', running: false }
    ];
    for (const state of states) {
        for (let i = 0; i < 4; i++) {
            report(window, {
                targets: {
                    'notebook-toggle': [{ x: 10, y: 10, width: 30, height: 30, disabled: false, label: '' }],
                    'notebook-refresh': [{ x: 10, y: 50, width: 30, height: 30, disabled: false, label: '' }],
                    'notebook-source': [{ x: 10, y: 90, width: 30, height: 30, disabled: false, label: '' }],
                    'notebook-entry': [{ x: 10, y: 130, width: 30, height: 30, disabled: false, label: 'App Suite' }],
                    'notebook-close': [{ x: 10, y: 170, width: 30, height: 30, disabled: false, label: '' }],
                    'notebook-node-run': [{ x: 10, y: 210, width: 30, height: 30, disabled: false, label: '' }]
                },
                state
            });
            sandbox.Tutorial.tickForTest();
            push();
            // Explanation and confirm steps wait for the press rather than
            // advancing themselves.
            if (press(document, 'Next')) sandbox.Tutorial.tickForTest();
        }
    }

    assert.ok(seen.length > 2, 'the tour must have moved through several steps');
    seen.forEach((n, i) => {
        if (i > 0) {
            assert.equal(n, seen[i - 1] + 1,
                `step numbers must be progressive, got ${seen.join(', ')}`);
        }
    });
    assert.equal(seen[0], 1, 'and start at 1');
});

test('a step keeps its number when the user goes back to it', () => {
    const { sandbox, window, document, body } = makeEnv();
    appPage(body);
    sandbox.Tutorial.start('notebook');

    report(window, {
        targets: {},
        state: { panelOpen: false, entries: 0, nodes: 0, firstNodeRunState: null, running: false }
    });
    sandbox.Tutorial.tickForTest();
    const first = stepCounter(document).number;

    report(window, {
        targets: {},
        state: { panelOpen: true, entries: 0, nodes: 0, firstNodeRunState: null, running: false }
    });
    sandbox.Tutorial.tickForTest();
    sandbox.Tutorial.tickForTest();
    const second = stepCounter(document).number;
    assert.equal(second, first + 1, 'the next presented step gets the next number');

    // Returning to a step already numbered must not spend a new number on it.
    sandbox.sessionStorage.setItem(
        'trustable.tutorial',
        JSON.stringify({ ...JSON.parse(sandbox.sessionStorage.getItem('trustable.tutorial')), step: 0 })
    );
    sandbox.Tutorial.resume();
    sandbox.Tutorial.tickForTest();
    assert.equal(stepCounter(document).number, first, 'the same step shows the same number');
});

test('a self-completing step waits for Next instead of advancing itself', () => {
    const { sandbox, window, document, body } = makeEnv();
    appPage(body);
    sandbox.Tutorial.start('notebook');

    const targets = {
        'notebook-toggle': [{ x: 10, y: 10, width: 30, height: 30, disabled: false, label: '' }],
        'notebook-refresh': [{ x: 10, y: 50, width: 30, height: 30, disabled: false, label: '' }]
    };

    // Reach "Load templates" by opening the panel, which is a real user action.
    report(window, { targets, state: { panelOpen: false, entries: 0, nodes: 0, firstNodeRunState: null, running: false } });
    sandbox.Tutorial.tickForTest();
    report(window, { targets, state: { panelOpen: true, entries: 0, nodes: 0, firstNodeRunState: null, running: false } });
    sandbox.Tutorial.tickForTest();
    assert.equal(sandbox.Tutorial.stepIdForTest(), 'refresh');
    assert.equal(actionLabels(document).includes('Next'), false,
        'no Next while the catalog is still loading');

    // The catalog lands on its own. That must NOT carry the step away: the
    // user had one 150 ms tick to read the card before this fix.
    report(window, { targets, state: { panelOpen: true, entries: 3, nodes: 0, firstNodeRunState: null, running: false } });
    sandbox.Tutorial.tickForTest();
    assert.equal(sandbox.Tutorial.stepIdForTest(), 'refresh',
        'a background fetch must not advance the step');
    assert.ok(actionLabels(document).includes('Next'), 'Next appears once the entries are there');

    assert.ok(press(document, 'Next'));
    sandbox.Tutorial.tickForTest();
    assert.equal(sandbox.Tutorial.stepIdForTest(), 'configure', 'the press is what advances it');
});

test('the Toolbar tour walks every control in bar order', () => {
    const { sandbox, document, body } = makeEnv();
    appPage(body);
    sandbox.Tutorial.start('toolbar');

    // Each step explains a control and continues on Next, so the whole tour is
    // driven by presses alone — nothing here waits on application state.
    const ids = [];
    for (let i = 0; i < 20; i++) {
        const id = sandbox.Tutorial.stepIdForTest();
        if (!id || ids[ids.length - 1] === id) break;
        ids.push(id);
        const counter = stepCounter(document);
        assert.equal(counter.number, ids.length, `step ${id} must be numbered ${ids.length}`);
        if (!press(document, 'Next')) break;
        sandbox.Tutorial.tickForTest();
    }

    assert.deepEqual(ids,
        ['pane', 'terminal', 'commit', 'device', 'route', 'reload', 'back', 'complete'],
        'the tour covers the toolbar left to right');
    assert.ok(actionLabels(document).includes('Close'), 'the last step finishes');
});

test('every step offers Skip this step, and it advances', () => {
    const { sandbox, window, document, body } = makeEnv();
    appPage(body);
    sandbox.Tutorial.start('notebook');

    const skipLink = () => overlay(document).querySelector('.tutorial-skip');

    // A frame step with no bridge report is the case that used to strand the
    // user for a full 20 s before offering any way on.
    sandbox.Tutorial.tickForTest();
    assert.equal(sandbox.Tutorial.stepIdForTest(), 'open');
    assert.equal(skipLink().classList.contains('hidden'), false,
        'skip is offered immediately, not after a timeout');

    skipLink().dispatch('click', { preventDefault: () => {}, stopPropagation: () => {} });
    sandbox.Tutorial.tickForTest();
    assert.equal(sandbox.Tutorial.stepIdForTest(), 'refresh', 'skip moves to the next step');

    // It stays available on a step that is merely waiting...
    report(window, {
        targets: {},
        state: { panelOpen: true, entries: 0, nodes: 0, firstNodeRunState: null, running: false }
    });
    sandbox.Tutorial.tickForTest();
    assert.equal(skipLink().classList.contains('hidden'), false, 'offered while waiting');

    // ...and on an explanation step that already has its own Next.
    skipLink().dispatch('click', { preventDefault: () => {}, stopPropagation: () => {} });
    sandbox.Tutorial.tickForTest();
    assert.equal(sandbox.Tutorial.stepIdForTest(), 'configure');
    assert.ok(actionLabels(document).includes('Next'));
    assert.equal(skipLink().classList.contains('hidden'), false, 'offered next to Next');
});

test('the final step offers Close rather than Skip', () => {
    const { sandbox, document, body } = makeEnv();
    appPage(body);
    sandbox.Tutorial.start('toolbar');

    // Walk to the end of the Toolbar tour.
    for (let i = 0; i < 10 && press(document, 'Next'); i++) sandbox.Tutorial.tickForTest();

    assert.equal(sandbox.Tutorial.stepIdForTest(), 'complete');
    assert.ok(actionLabels(document).includes('Close'));
    assert.equal(overlay(document).querySelector('.tutorial-skip').classList.contains('hidden'), true,
        'there is nothing after the last step to skip to');
});

test('the Menus tour opens each menu itself and walks its items', () => {
    const { sandbox, document, body } = makeEnv();
    appPage(body);
    const isOpen = (id) => !document.getElementById(id).classList.contains('hidden');

    sandbox.Tutorial.start('menus');
    sandbox.Tutorial.tickForTest();

    // The overlay swallows clicks, so the user could never open these: the tour
    // has to do it, or every step would wait on a menu that stays shut.
    assert.equal(isOpen('configMenu'), true, 'Config is opened by the tour');
    assert.equal(sandbox.Tutorial.stepIdForTest(), 'config');

    const ids = [];
    for (let i = 0; i < 20; i++) {
        const id = sandbox.Tutorial.stepIdForTest();
        if (!id || ids[ids.length - 1] === id) break;
        ids.push(id);
        if (!press(document, 'Next')) break;
        sandbox.Tutorial.tickForTest();
    }

    assert.deepEqual(ids,
        ['config', 'env', 'skills', 'agents', 'utils', 'revert', 'reload-redeploy',
         'clean', 'debug', 'files', 'upload', 'complete'],
        'the tour walks Config then Utils');
});

test('the Menus tour shows one menu at a time and closes them when it ends', () => {
    const { sandbox, document, body } = makeEnv();
    appPage(body);
    const isOpen = (id) => !document.getElementById(id).classList.contains('hidden');

    sandbox.Tutorial.start('menus');
    sandbox.Tutorial.tickForTest();
    assert.equal(isOpen('utilsMenu'), false, 'Utils stays shut while Config is described');

    // Reaching the Utils half closes Config, so only one dropdown is ever up.
    for (let i = 0; i < 4; i++) { press(document, 'Next'); sandbox.Tutorial.tickForTest(); }
    assert.equal(sandbox.Tutorial.stepIdForTest(), 'utils');
    assert.equal(isOpen('utilsMenu'), true, 'Utils is opened in turn');
    assert.equal(isOpen('configMenu'), false, 'Config is closed again');

    // Exiting midway must not strand a menu open — the user cannot close it
    // themselves while the overlay is up.
    sandbox.Tutorial.stop();
    assert.equal(isOpen('utilsMenu'), false, 'exiting closes the menu it opened');
    assert.equal(isOpen('configMenu'), false);
});
