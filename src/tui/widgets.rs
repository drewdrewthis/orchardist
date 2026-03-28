//! Reusable Ratatui widget primitives.
//!
//! Currently exposes `render_popup`, a helper that centres a bordered,
//! padded popup block over the terminal area. Used by both the list view
//! and the dialog module to render modal overlays.
use ratatui::prelude::*;
use ratatui::widgets::*;

// ---------------------------------------------------------------------------
// Popup rendering helper
// ---------------------------------------------------------------------------

/// Renders a centered rounded-border popup over the current frame.
///
/// `percent_x` controls popup width as a percentage of the terminal width.
/// `height` is the absolute row count; it is clamped to the terminal height.
/// `bg_color` fills the popup background (typically `theme.background`).
#[allow(clippy::too_many_arguments)]
pub fn render_popup(
    f: &mut Frame,
    lines: Vec<Line>,
    border_color: Color,
    bg_color: Color,
    percent_x: u16,
    height: u16,
    title: Option<&str>,
    padding: Padding,
) {
    let mut block = Block::default()
        .borders(Borders::ALL)
        .border_style(Style::default().fg(border_color).bg(bg_color))
        .border_type(BorderType::Rounded)
        .padding(padding);
    if let Some(t) = title {
        block = block.title(t).title_style(
            Style::default()
                .fg(border_color)
                .bg(bg_color)
                .add_modifier(Modifier::BOLD),
        );
    }
    let content = Paragraph::new(lines)
        .style(Style::default().bg(bg_color))
        .block(block);
    let popup = centered_rect(percent_x, height, f.area());
    f.render_widget(Clear, popup);
    f.render_widget(content, popup);
}

/// Returns a centered rectangle within `r`, constrained by percent width and absolute height.
pub fn centered_rect(percent_x: u16, height: u16, r: Rect) -> Rect {
    let popup_width = r.width * percent_x / 100;
    let popup_height = height.min(r.height);
    let x = (r.width.saturating_sub(popup_width)) / 2;
    let y = (r.height.saturating_sub(popup_height)) / 2;
    Rect::new(r.x + x, r.y + y, popup_width, popup_height)
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn centered_rect_smaller_than_area() {
        let area = Rect::new(0, 0, 100, 40);
        let popup = centered_rect(70, 12, area);
        assert_eq!(popup.width, 70);
        assert_eq!(popup.height, 12);
        assert_eq!(popup.x, 15);
        assert_eq!(popup.y, 14);
    }

    #[test]
    fn centered_rect_height_clamped() {
        let area = Rect::new(0, 0, 100, 5);
        let popup = centered_rect(70, 12, area);
        assert_eq!(popup.height, 5);
    }
}
