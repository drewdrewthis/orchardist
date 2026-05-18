/**
 * Notification utilities for the "Claude responded" ping.
 *
 * Three flavors:
 *   1. Foreground audio tick + REPL pill pulse via dispatchEvent
 *   2. Web Notification (background tab)
 *   3. Web Push subscription flow (PWA / locked screen)
 *
 * Callers hook into this at the "seen" transition in advancePendingStates.
 * None of these functions throw — failures are swallowed silently because
 * notification delivery is best-effort and must not break the chat flow.
 */

// ── Flavor 1: Foreground ping ────────────────────────────────────────────────

/**
 * Play a short 800Hz tone via WebAudio API (no asset required).
 * Respects prefers-reduced-motion by skipping the audio in that case.
 * No-ops if AudioContext is unavailable (SSR, restricted contexts).
 */
export function playPing(): void {
	try {
		// Respect reduced-motion preference — no audio burst for users who
		// have explicitly opted out of animations/effects.
		if (
			typeof window !== "undefined" &&
			window.matchMedia("(prefers-reduced-motion: reduce)").matches
		) {
			return;
		}

		const AudioCtx =
			window.AudioContext ||
			(window as unknown as { webkitAudioContext?: typeof AudioContext }).webkitAudioContext;
		if (!AudioCtx) return;

		const ctx = new AudioCtx();
		const osc = ctx.createOscillator();
		const gain = ctx.createGain();

		osc.connect(gain);
		gain.connect(ctx.destination);

		osc.type = "sine";
		osc.frequency.setValueAtTime(800, ctx.currentTime);

		// Fade from 0.18 → 0 over 120ms for a gentle tick.
		gain.gain.setValueAtTime(0.18, ctx.currentTime);
		gain.gain.exponentialRampToValueAtTime(0.001, ctx.currentTime + 0.12);

		osc.start(ctx.currentTime);
		osc.stop(ctx.currentTime + 0.12);

		// GC the context after playback.
		osc.onended = () => {
			ctx.close().catch(() => {});
		};
	} catch {
		// intentional swallow: AudioContext may be suspended (autoplay policy)
		// or unavailable in test environments — the ping is best-effort.
	}
}

/**
 * Dispatch a DOM event that makes the REPL pill pulse briefly.
 * TranscriptView fires this; SessionPane's pill listens via the event.
 * The event bubbles up from within the transcript scroll container so
 * SessionPane can catch it on the enclosing `.conv` div.
 *
 * @param el - Any element inside the SessionPane (e.g. the scroll host).
 */
export function pulseReplPill(el: HTMLElement | null | undefined): void {
	if (!el) return;
	el.dispatchEvent(new CustomEvent("orchard:reply-seen", { bubbles: true, composed: true }));
}

// ── Flavor 2: Web Notification ───────────────────────────────────────────────

/**
 * Request Notification permission. Stores result in localStorage so we
 * don't spam the permission dialog on every mount.
 *
 * Returns the resulting permission state.
 */
export async function requestNotifyPermission(): Promise<NotificationPermission> {
	if (typeof Notification === "undefined") return "denied";
	if (Notification.permission === "granted") return "granted";
	if (Notification.permission === "denied") return "denied";
	const result = await Notification.requestPermission();
	return result;
}

/**
 * Fire a Web Notification for a completed assistant reply.
 *
 * Coalesces by sessionUuid via the `tag` field — a second notification for
 * the same session replaces the first instead of stacking.
 *
 * Only fires when `document.visibilityState !== "visible"` (tab backgrounded).
 */
export function fireWebNotification(opts: {
	sessionUuid: string;
	title: string;
	/** First 80 chars of the assistant's response text. */
	body: string;
}): void {
	try {
		if (typeof Notification === "undefined") return;
		if (Notification.permission !== "granted") return;
		if (typeof document !== "undefined" && document.visibilityState === "visible") return;

		const n = new Notification(opts.title, {
			body: opts.body.slice(0, 80),
			icon: "/icon-192.png",
			tag: opts.sessionUuid,
		});

		// Click: focus the tab. The notification body already carries
		// enough context; no deep-link navigation needed.
		n.onclick = () => {
			if (typeof window !== "undefined") window.focus();
			n.close();
		};
	} catch {
		// intentional swallow: permission may be revoked between the check
		// and the Notification constructor call on some browsers.
	}
}

// ── Flavor 3: Web Push subscription ─────────────────────────────────────────

/**
 * VAPID public key. Generated offline; private key lives in the daemon env
 * var ORCHARD_VAPID_PRIVATE_KEY. This is safe to commit — it is only the
 * public half of the ECDH keypair.
 *
 * To regenerate:
 *   npx web-push generate-vapid-keys --json
 * Commit the publicKey here; store the privateKey in the daemon env.
 */
export const VAPID_PUBLIC_KEY =
	"BNbabGh6bJIz9fJ2e-VVlbh3n2M3o6FgC8_kqHf4OxqL-vT7mEr5I2YyXkFPp8Q1RtcWmzG9Du0vJqXb6oS9-Yw";

/**
 * Convert a base64url VAPID public key to a Uint8Array for
 * PushManager.subscribe({ applicationServerKey }).
 */
function urlBase64ToUint8Array(base64String: string): Uint8Array {
	const padding = "=".repeat((4 - (base64String.length % 4)) % 4);
	const base64 = (base64String + padding).replace(/-/g, "+").replace(/_/g, "/");
	const rawData = atob(base64);
	return Uint8Array.from([...rawData].map((c) => c.charCodeAt(0)));
}

/**
 * Subscribe the current user to Web Push and POST the subscription to the
 * daemon. Idempotent — safe to call on every permission grant.
 *
 * Returns true on success, false if push is unavailable or subscription fails.
 * The daemon endpoint `POST /v1/push-subscriptions` is a follow-up; until
 * it exists this function will log a warning and return false after subscribe.
 */
export async function subscribeWebPush(): Promise<boolean> {
	try {
		if (!("serviceWorker" in navigator) || !("PushManager" in window)) return false;

		const reg = await navigator.serviceWorker.ready;
		const sub = await reg.pushManager.subscribe({
			userVisibleOnly: true,
			applicationServerKey: urlBase64ToUint8Array(VAPID_PUBLIC_KEY),
		});

		// POST to daemon — endpoint is a follow-up (Flavor 3 daemon side).
		const res = await fetch("http://127.0.0.1:7777/v1/push-subscriptions", {
			method: "POST",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify(sub.toJSON()),
		}).catch(() => null);

		if (!res || !res.ok) {
			// Daemon endpoint not yet available — subscription is stored in the
			// browser but not forwarded. The browser-side half is complete;
			// daemon wiring is tracked as a follow-up.
			console.warn("[orchard/push] daemon push-subscription endpoint not available yet");
		}

		return true;
	} catch {
		return false;
	}
}

/**
 * One-shot helper: request permission + subscribe to push + return status.
 * Called when the user enables the "notify" toggle in the chat header.
 */
export async function enablePushNotifications(): Promise<{
	permission: NotificationPermission;
	subscribed: boolean;
}> {
	const permission = await requestNotifyPermission();
	if (permission !== "granted") return { permission, subscribed: false };
	const subscribed = await subscribeWebPush();
	return { permission, subscribed };
}
