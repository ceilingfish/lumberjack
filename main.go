// Command lumberjack is the CLI and background daemon for tracking a GitHub
// repository's open PRs and reconciling git worktrees. See AGENTS.md.
package main

import "github.com/ceilingfish/lumberjack/cmd"

func main() {
	cmd.Execute()
}
