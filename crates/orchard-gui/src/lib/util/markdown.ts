/**
 * Markdown rendering for chat transcript + composer preview.
 *
 * Why client-side: messages come from the daemon's jsonl as plain text;
 * the daemon never renders markdown. Browser renders to HTML, DOMPurify
 * cleans it.
 *
 * Configured for chat-shape content:
 *   - GFM (tables, strikethrough, task lists, autolinks)
 *   - Line breaks treated as <br> (chat-natural)
 *   - No mangling (preserves emails / mentions)
 */
import { marked } from "marked";
import DOMPurify from "dompurify";

marked.setOptions({
	gfm: true,
	breaks: true,
});

const PURIFY_CONFIG = {
	ALLOWED_TAGS: [
		"a", "b", "blockquote", "br", "code", "del", "em", "h1", "h2", "h3",
		"h4", "h5", "h6", "hr", "i", "img", "li", "ol", "p", "pre", "s",
		"strong", "sub", "sup", "table", "tbody", "td", "th", "thead", "tr",
		"ul", "span", "div",
	],
	ALLOWED_ATTR: ["href", "title", "alt", "src", "class", "lang"],
	ALLOW_DATA_ATTR: false,
	RETURN_TRUSTED_TYPE: false,
};

/**
 * Render a markdown string to sanitized HTML. Returns "" for empty input.
 */
export function renderMarkdown(src: string | null | undefined): string {
	if (!src) return "";
	try {
		const raw = marked.parse(src, { async: false }) as string;
		// DOMPurify returns `TrustedHTML` when TT is enabled; we explicitly
		// disable that above, so the result is a plain string.
		return DOMPurify.sanitize(raw, PURIFY_CONFIG) as unknown as string;
	} catch {
		// Bad input — fall back to escaped plain text.
		const escaped = src
			.replace(/&/g, "&amp;")
			.replace(/</g, "&lt;")
			.replace(/>/g, "&gt;");
		return `<p>${escaped.replace(/\n/g, "<br>")}</p>`;
	}
}
