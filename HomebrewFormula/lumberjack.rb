# frozen_string_literal: true

class Lumberjack < Formula
  desc "Track open PRs and reconcile git worktrees"
  homepage "https://github.com/ceilingfish/lumberjack"
  url "https://github.com/ceilingfish/lumberjack/archive/refs/tags/v0.4.0.tar.gz"
  sha256 "bd526d20b025758c61771c6938299714f0ee2b1f43f6803fe6784d39c53da133"
  head "https://github.com/ceilingfish/lumberjack.git", branch: "main"

  depends_on "go" => :build
  depends_on "gh"

  def install
    ldflags = "-X github.com/ceilingfish/lumberjack/internal/cli.Version=v#{version}"
    system "go", "build", *std_go_args(ldflags:)
    generate_completions_from_executable(bin/"lumberjack", "completion")
  end

  def caveats
    <<~EOS
      Homebrew has installed the CLI only. To register the background daemon as a
      per-user LaunchAgent (no sudo), run:

        lumberjack install --daemon-only
        lumberjack daemon start

      The daemon is registered against the binary path it sees at that moment,
      which moves when Homebrew installs a new version. After `brew upgrade
      lumberjack`, re-register it:

        lumberjack install --daemon-only --force

      `gh` must be authenticated (`gh auth login`) — Lumberjack reuses that login.
    EOS
  end

  test do
    assert_match "Track open PRs", shell_output("#{bin}/lumberjack --help")
    assert_match "compdef", shell_output("#{bin}/lumberjack completion zsh")
    system bin/"lumberjack", "completion", "bash"
  end
end
