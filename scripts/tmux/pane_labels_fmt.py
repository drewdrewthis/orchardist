# --- tmux format-string safety ---------------------------------------------
# choose-tree renders this label through `#{E:@orchard_pane_label}`, which
# expands the option's value as a tmux format — and `#(cmd)` in a format runs
# `cmd`. Branch names, issue titles, PR labels, hook-state fields and pane
# paths are all attacker-influenceable, so none may reach the option with a
# live `#`. tmux's only escape is doubling it. Verified on tmux 3.6a: a raw
# `#(touch F)` planted in the option creates F on the first real render, while
# the doubled form renders as inert literal text.


def fmt_escape(value):
    """Neutralise every tmux format directive in untrusted text."""
    return str(value).replace("#", "##")


def path_under(parent, child):
    """True when `child` is `parent` itself or lives beneath it.

    The boundary is a path separator, not a string prefix, so
    `/a/b` never claims `/a/bc`. `/` (and any trailing-slash form of it) is
    the parent of every absolute path: naive `parent + "/"` concatenation
    would build `//`, which no absolute path starts with.
    """
    if not parent or not child:
        return False
    parent = parent.rstrip("/")
    if not parent:
        return child.startswith("/")
    return child == parent or child.startswith(parent + "/")


def cell(style, text):
    """Build one label cell.

    `style` is ours and stays live; `text` is untrusted and is escaped. Cells
    are only ever built here, so no caller can leak an unescaped value into
    the option by hand.
    """
    return f"#[{style}]{fmt_escape(text)}#[default]"
