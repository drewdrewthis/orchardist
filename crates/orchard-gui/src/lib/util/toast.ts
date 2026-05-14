/**
 * Thin wrapper over svelte-sonner's `toast` that:
 *   - Extracts a human-readable message from any thrown value
 *   - Logs the full error (with stack) to the dev-tools console so engineers
 *     can trace the cause even after the toast auto-dismisses
 */
import { toast as _toast } from "svelte-sonner";

function extractMessage(err: unknown): string {
	if (err instanceof Error) return err.message;
	if (typeof err === "string") return err;
	try {
		return JSON.stringify(err);
	} catch {
		return String(err);
	}
}

export const toast = {
	/**
	 * Surface an error as a visible toast and emit the full error to the
	 * dev-tools console so the stack trace is preserved.
	 */
	error(err: unknown): void {
		console.error(err);
		_toast.error(extractMessage(err));
	},
	/** Convenience pass-throughs kept thin — add only what's needed (YAGNI). */
	success: _toast.success.bind(_toast),
	info: _toast.info.bind(_toast),
};
