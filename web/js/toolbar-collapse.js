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

	/*
	 * WHY we sum the children instead of reading bar.scrollWidth: the top bar
	 * separates its two button groups with a `flex-1` spacer. A flexible spacer
	 * absorbs all the slack and then shrinks to zero under pressure, so the
	 * content never reports as wider than the container — scrollWidth stays
	 * equal to clientWidth at every window size, and with `overflow: hidden` on
	 * the page the groups are simply clipped instead. Measuring the space the
	 * children actually need is the only reading that detects this.
	 *
	 * WHY we recurse instead of reading each child's scrollWidth: the buttons
	 * are not direct children of the bar, they sit inside `shrink-0` group
	 * wrappers. A flex item that cannot shrink is laid out at its full content
	 * width and overflows the *container*, but its own box is not scrollable —
	 * its buttons fit inside it exactly — so scrollWidth == clientWidth for the
	 * wrapper and the overflow is invisible to that reading. Descending to the
	 * leaves measures the space actually needed regardless of whether any
	 * wrapper in between happens to be shrinkable.
	 */

	/*
	 * A leaf is something we measure whole rather than descend into: a control
	 * and its internal icon/label, or any element with no element children.
	 * Descending into a button would sum its icon and label as if they were
	 * siblings to be laid out, which is not what the button's own box needs.
	 */
	function isLeaf(el) {
		if (!el.firstElementChild) return true;
		if (el.tagName === "BUTTON") return true;
		return (
			el.classList.contains("nu-btn") ||
			el.classList.contains("nu-status-pill") ||
			el.getAttribute("role") === "group"
		);
	}

	/*
	 * WHY getBoundingClientRect and not offsetWidth: offsetWidth rounds to
	 * whole pixels. Rounding ~10 buttons down discards enough width to hide a
	 * real overflow, which is exactly the case we are trying to detect.
	 */
	function measure(el) {
		if (isLeaf(el)) return el.getBoundingClientRect().width;

		var style = getComputedStyle(el);
		var gap = parseFloat(style.columnGap || style.gap) || 0;
		var total = 0;
		var counted = 0;
		Array.prototype.forEach.call(el.children, function (child) {
			var childStyle = getComputedStyle(child);
			// Out of flow: an open dropdown menu is a child of a `relative`
			// wrapper inside the bar, but it floats over the page and takes no
			// space in it. Counting it would collapse the bar whenever a menu
			// happens to be open.
			var position = childStyle.position;
			if (position === "absolute" || position === "fixed") return;
			if (childStyle.display === "none") return;
			// The flexible spacer contributes nothing of its own.
			if (parseFloat(childStyle.flexGrow) > 0) return;
			total += measure(child);
			counted++;
		});
		if (counted > 1) total += gap * (counted - 1);
		total += parseFloat(style.paddingLeft) + parseFloat(style.paddingRight);
		return total;
	}

	function contentWidth(bar) {
		return measure(bar);
	}

	function available(bar) {
		return bar.clientWidth;
	}

	/*
	 * Two stages. Collapsing the buttons alone still leaves the app name, the
	 * git status text and the device toggle, which together need ~370px — on a
	 * genuinely small window the bar is still clipped. `is-collapsed-tight`
	 * drops those too.
	 *
	 * Every decision is made from the widest state, so the measurement never
	 * depends on which state we happen to be in when called. The class list is
	 * written once at the end, and any intermediate state is applied and read
	 * back within a single frame, so nothing intermediate is painted.
	 */
	function update(bar) {
		if (!bar.isConnected || !bar.clientWidth) return;

		var wasCollapsed = bar.classList.contains("is-collapsed");
		var wasTight = bar.classList.contains("is-collapsed-tight");

		// Measure expanded.
		bar.classList.remove("is-collapsed", "is-collapsed-tight");
		var room = available(bar);
		var neededExpanded = contentWidth(bar);

		// Expanding again needs a margin so a bar that fits by a hair does not
		// flip-flop on sub-pixel rounding; staying expanded does not.
		var expandedFits = wasCollapsed
			? neededExpanded <= room - EXPAND_MARGIN_PX
			: neededExpanded <= room;

		var collapsed = false;
		var tight = false;
		if (!expandedFits) {
			collapsed = true;
			bar.classList.add("is-collapsed");
			var neededCollapsed = contentWidth(bar);
			var collapsedFits = wasTight
				? neededCollapsed <= room - EXPAND_MARGIN_PX
				: neededCollapsed <= room;
			if (!collapsedFits) tight = true;
		}

		bar.classList.toggle("is-collapsed", collapsed);
		bar.classList.toggle("is-collapsed-tight", tight);
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
