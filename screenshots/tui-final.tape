# VHS tape file for devx TUI walkthrough
# Run setup-demo.sh before recording, restore-demo.sh after

Output tui-final.gif

# Configure terminal appearance
Set FontSize 18
Set Width 1400
Set Height 900
Set Padding 20
Set Theme "Catppuccin Mocha"
Set Shell "bash"

# Hide command typing - start directly at TUI
Hide
Type "devx"
Enter
# Wait longer for TUI to fully load
Sleep 5s
Show

# Show the main TUI interface for a moment
Sleep 1.5s

# Navigate through sessions
Down
Sleep 1s
Down
Sleep 1s

# Toggle preview panel (p key)
Type "p"
Sleep 2s

# Navigate with preview visible
Down
Sleep 1s
Up
Sleep 1s

# View hostnames for the selected session (h key)
Type "h"
Sleep 2.5s

# Go back to main list (ESC)
Escape
Sleep 1s

# Toggle preview off
Type "p"
Sleep 1s

# Navigate to a different session
Down
Sleep 1s
Down
Sleep 1s

# Show create new session dialog (c key)
Type "c"
Sleep 2s

# Cancel creation (ESC)
Escape
Sleep 1s

# Navigate more
Up
Sleep 800ms

# Final pause to show the interface
Sleep 2s

# Exit
Type "q"
Sleep 500ms
