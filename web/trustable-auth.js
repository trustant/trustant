(function () {
    'use strict';

    const nativeFetch = window.fetch.bind(window);
    let sessionPromise = null;

    function requestDetails(input, init) {
        const request = input instanceof Request ? input : null;
        const url = new URL(request ? request.url : String(input), window.location.href);
        const method = String((init && init.method) || (request && request.method) || 'GET').toUpperCase();
        return { url, method };
    }

    function isEffectful(details) {
        if (details.url.origin !== window.location.origin || !details.url.pathname.startsWith('/api/')) {
            return false;
        }
        if (!['GET', 'HEAD', 'OPTIONS'].includes(details.method)) {
            return details.url.pathname !== '/api/auth/login';
        }
        if (details.method !== 'GET') return false;
        return details.url.pathname.startsWith('/api/launch/') ||
            ['/api/configure', '/api/ollama-connect', '/api/redeploy'].includes(details.url.pathname);
    }

    async function loadSession(force) {
        if (!sessionPromise || force) {
            sessionPromise = nativeFetch('/api/auth/session', {
                method: 'GET',
                credentials: 'same-origin',
                headers: { 'Accept': 'application/json' }
            }).then(async response => {
                if (response.status === 401) {
                    redirectToLogin();
                    throw new Error('Authentication required');
                }
                if (!response.ok) return { enabled: false };
                return response.json();
            }).catch(error => {
                sessionPromise = null;
                throw error;
            });
        }
        return sessionPromise;
    }

    function redirectToLogin() {
        if (window.location.pathname === '/login.html') return;
        const next = window.location.pathname + window.location.search + window.location.hash;
        window.location.replace('/login.html?next=' + encodeURIComponent(next));
    }

    window.fetch = async function trustableFetch(input, init) {
        const details = requestDetails(input, init);
        let nextInput = input;
        let nextInit = init ? Object.assign({}, init) : {};

        if (isEffectful(details)) {
            const session = await loadSession(false);
            if (session.enabled) {
                const headers = new Headers(input instanceof Request ? input.headers : undefined);
                if (nextInit.headers) {
                    new Headers(nextInit.headers).forEach((value, key) => headers.set(key, value));
                }
                headers.set('X-Trustable-CSRF', session.csrf);
                nextInit.headers = headers;
                nextInit.credentials = 'same-origin';
                if (input instanceof Request) {
                    nextInput = new Request(input, nextInit);
                    nextInit = undefined;
                }
            }
        }

        const response = await nativeFetch(nextInput, nextInit);
        if (response.status === 401 && details.url.origin === window.location.origin && details.url.pathname.startsWith('/api/')) {
            sessionPromise = null;
            redirectToLogin();
        }
        return response;
    };

    async function logout() {
        const response = await window.fetch('/api/auth/logout', { method: 'POST' });
        sessionPromise = null;
        if (!response.ok && response.status !== 401) {
            throw new Error(await response.text() || 'Logout failed');
        }
        window.location.replace('/login.html');
    }

    function revealLogout(session) {
        if (!session || !session.enabled) return;
        document.querySelectorAll('[data-trustable-logout]').forEach(button => {
            button.classList.remove('hidden');
        });
    }

    window.TrustableAuth = { loadSession, logout };
    document.addEventListener('DOMContentLoaded', () => {
        if (document.querySelector('[data-trustable-logout]')) {
            loadSession(false).then(revealLogout).catch(() => {});
        }
    });
})();
