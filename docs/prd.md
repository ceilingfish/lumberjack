## Purpose

I want to build a tool that keeps track of my worktrees for a specific repository. It should understand when a repository is in github and keep track of which PRs are open.

Whenever a PR is opened in a repository that lumberjack is tracking it should create a new worktree to track that branch.

If our repo is in /path/to/my_repo, I want you to create new worktree clones in /path/to/my_repo-branch-name. Obviously some branch names aren't going to be valid file names, here's some examples of how we should resolve this.

| Branch name          | Directory name     |
| -------------------- | ------------------ |
| feature/my-feature   | my_repo-my-feature |
| my-feature           | my_repo-my-feature |
| fix/#12345-bugfix-id | my_repo-bugfix-id  |

Lumberjack should be initialised in a repository by doing the following

```
cd /path/to/my_repo
lumberjack init .
```

Once this is done, it should check to see if this is a github repository, if it is, then it should go through a process to authenticate with the github API and establish that it can get the status of PRs.

Once this has been established then lumberjack should store the context of this repository in a database, so that it knows which repos it is tracking.

A background daemon process should query this database on an hourly basis and check for open PRs. Any PRs that are open should have their branches downloaded and checked out as described above. Any branches that are currently checked out but their PRs have been merged or closed should be removed. If a worktree has changes locally that are not in the merged branch, then it shouldn't be deleted, these should be considered in need of reconciliation.

## Architecture: client / server split

Lumberjack runs as two cooperating processes:

- **The daemon is the server and the sole owner of state.** It owns the SQLite database, runs schema migrations on startup, drives the hourly sync loop, and performs every worktree operation (create, checkout, delete, and reconciliation checks). Because only the daemon touches the database and the working trees, there is a single writer — no two processes can race on the DB or corrupt a worktree. The daemon is started with `lumberjack daemon`.
- **The CLI is a thin client.** Every user-facing command (`list`, `status`, `sync`, etc.) is a request to the daemon. The CLI never opens the database or shells out to `git` for worktrees itself; it asks the daemon to do the work and renders the result. If the daemon is not running, the CLI says so clearly rather than silently operating on stale or half-owned state.

The two processes communicate over a **gRPC API** whose service and message definitions are the contract between them. The daemon exposes this API; the CLI consumes it through a generated gRPC client published in `pkg/client/`, so any other tooling that wants to talk to a Lumberjack daemon can reuse the same client.

Because client and server always run on the same machine, the daemon listens on a **local Unix domain socket** (under `~/.lumberjack/` by default) rather than a public network port.

## Commands

`lumberjack list` should show the list of tracked repositories
`lumberjack status [--repository NAME]` should show details of the last sync for the tracked repository at the current working directory, or for the named repository when `--repository NAME` is given
`lumberjack sync [--repository NAME]` synchronises worktrees for the current-directory repo, or the named repository, specifically
`lumberjack worktrees [--repository NAME]` should show a list of worktrees checked out for the current-directory repo, or the named repository, with their path, and if they need reconciliation, a warning
`lumberjack worktree add BRANCH [--repository NAME]` creates a worktree for BRANCH on demand, in the same conventional location (`<worktree parent>/<dir prefix>-<slug>`) a sync would choose, and runs the repository's setup steps against it. BRANCH is checked out tracking the remote branch when one exists, from an existing local branch when not, and otherwise created off the repository's default branch — so a brand-new feature branch can be started with one command. The worktree is tracked with no PR attached; a later sync links it to a PR when one opens for the branch. A setup-step failure is reported but does not undo the worktree.
`lumberjack worktree delete BRANCH_OR_DIRECTORY_NAME [--repository NAME]` will delete the worktree, if the tip of the local worktree doesn't match the tip merged remote, then we should ask for confirmation with a warning that the user will lose X commits
`lumberjack set-login [LOGIN] [--repository NAME]` sets the `gh` account the current-directory repository, or the named repository, operates under (the daemon switches to it before any git/gh operation and restores the prior account after). LOGIN must be an account `gh` is authenticated as for the repository's host, and the daemon verifies that account can actually reach the repository on GitHub before saving — otherwise the update is rejected. Omit LOGIN to pick from the authenticated accounts interactively (↑/↓, enter to confirm)
`lumberjack sync-all` Triggers a synchronisation of all repositories

Every scoped command above (`status`, `sync`, `worktrees`, `worktree add`, `worktree delete`, `set-login`) resolves its target the same way: `--repository NAME` when given, otherwise the tracked repository at the current working directory. Neither present is a clear error rather than acting on the wrong repo.
`lumberjack daemon` Runs the background daemon (the gRPC server) in the foreground. This is the process that owns the database and worktrees and runs the hourly sync loop; normally it is managed by a service supervisor rather than invoked by hand.
`lumberjack doctor` Checks that the required host prerequisites are available and reports their location and version. It verifies that `git` and `gh` can be found (honouring `LUMBERJACK_GIT_PATH` and `LUMBERJACK_GITHUB_CLI_PATH`, otherwise searching the system `PATH`), and that `gh` is authenticated. Exits non-zero if any check fails, so it can be used in scripts.

## Environment variables

| Variable             | Default                   | Description                                                                                                            |
| -------------------- | ------------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| `LUMBERJACK_DB_PATH` | `~/.lumberjack/db.sqlite` | Path to the SQLite database tracking repositories and worktrees. The parent directory is created if it does not exist. |
| `LUMBERJACK_GIT_PATH` | Located on `PATH` (`git`) | Path to the `git` executable. If unset, the system `PATH` is searched. |
| `LUMBERJACK_GITHUB_CLI_PATH` | Located on `PATH` (`gh`) | Path to the GitHub CLI (`gh`) executable. If unset, the system `PATH` is searched. |
| `LUMBERJACK_SOCKET_PATH` | `~/.lumberjack/daemon.sock` | Path to the Unix domain socket the daemon listens on and the CLI dials. The parent directory is created if it does not exist. |
| `LUMBERJACK_PID_PATH` | `~/.lumberjack/daemon.pid` | Path to the daemon's PID file, used to detect whether a daemon is already running. The parent directory is created if it does not exist. |
