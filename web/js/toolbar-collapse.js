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
 * Toolbar overflow collapse — spec/3-app.md, spec/1-applist.md.
 *
 * Collapses a toolbar to icon-only when its buttons no longer fit, and expands
 * it again when they do. The labels become tooltips (the `title` attribute is
 * never touched, so it works in both states).
 *
 * WHY measured overflow and not a media query: the natural width of these bars
 * depends on content that varies per app — the application name, the optional
 * credits pill, the current route string. Any fixed breakpoint is wrong for
 * some apps in one direction or the other. Comparing scrollWidth against
 * clientWidth collapses exactly when the buttons would actually be cut off.
 */
(function () {
	"use strict";

	/*
	 * WHY hysteresis: collapsing removes the labels, which makes the bar
	 * narrower, which clears the very overflow that triggered it — expand, and
	 * the labels come back and it overflows again. That is an infinite loop
	 * driven by our own DOM writes.
	 *
	 * So the two directions use different tests. Collapsing is decided on real
	 * overflow. Expanding is decided on whether the *expanded* bar would fit,
	 * which we measure directly by removing the class, reading the width, and
	 * putting it back before the browser paints. The margin keeps a bar that
	 * fits by a hair from flip-flopping on sub-pixel rounding.
	 */
	var EXPAND_MARGIN_PX = 24;

	function overflows(bar) {
		return bar.scrollWidth > bar.clientWidth + 1;
	}

	function update(bar) {
		if (!bar.isConnected) return;

		if (!bar.classList.contains("is-collapsed")) {
			if (overflows(bar)) bar.classList.add("is-collapsed");
			return;
		}

		// Already collapsed: measure what the expanded bar would need. This
		// write/read/write happens synchronously inside one frame, so it is
		// never painted.
		bar.classList.remove("is-collapsed");
		var fitsExpanded = bar.scrollWidth <= bar.clientWidth - EXPAND_MARGIN_PX;
		if (!fitsExpanded) bar.classList.add("is-collapsed");
	}

	function watch(bar) {
		if (!bar || bar.dataset.collapseWatched === "1") return;
		bar.dataset.collapseWatched = "1";

		var queued = false;
		function schedule() {
			if (queued) return;
			queued = true;
			requestAnimationFrame(function () {
				queued = false;
				update(bar);
			});
		}

		if (typeof ResizeObserver === "function") {
			// Observing the bar catches window resizes; observing its children
			// catches content that changes width in place — the credits pill
			// appearing, the route label growing, Reload becoming Redeploy.
			var observer = new ResizeObserver(schedule);
			observer.observe(bar);
			Array.prototype.forEach.call(bar.children, function (child) {
				observer.observe(child);
			});
		}
		window.addEventListener("resize", schedule);
		schedule();
	}

	/** Watch every toolbar marked `data-collapse` on the page. */
	function initToolbarCollapse() {
		document
			.querySelectorAll("[data-collapse]")
			.forEach(watch);
	}

	window.initToolbarCollapse = initToolbarCollapse;
	// Re-measure on demand: callers that change toolbar contents (showing the
	// credits pill, setting the route) can force a recheck.
	window.refreshToolbarCollapse = function () {
		document.querySelectorAll("[data-collapse]").forEach(update);
	};

	if (document.readyState === "loading") {
		document.addEventListener("DOMContentLoaded", initToolbarCollapse);
	} else {
		initToolbarCollapse();
	}
})();
