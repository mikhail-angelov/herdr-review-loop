package loop

// PanelWidth returns the desired split width while protecting the author pane on
// small workspaces. It is intentionally pure so layout decisions are testable.
func PanelWidth(workspaceWidth int) int {
	if workspaceWidth < 64 {
		return 0
	}
	width := workspaceWidth * 30 / 100
	if width < 32 {
		width = 32
	}
	if width > 44 {
		width = 44
	}
	if width > workspaceWidth/2 {
		width = workspaceWidth / 2
	}
	return width
}

func ResizeDirection(current, target int) (string, int, bool) {
	if target == 0 || current-target >= -1 && current-target <= 1 {
		return "", 0, false
	}
	if current < target {
		return "right", target - current, true
	}
	return "left", current - target, true
}
